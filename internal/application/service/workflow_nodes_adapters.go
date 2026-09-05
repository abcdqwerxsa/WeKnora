package service

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
	"github.com/Tencent/WeKnora/internal/logger"

	// Blank import: registers the "duckdb" database/sql driver the DataOps
	// adapter opens (same driver the platform already links via container).
	_ "github.com/duckdb/duckdb-go/v2"
)

// workflowHTTPMaxRedirects bounds redirect hops; every hop re-validates the
// target host against the intranet-only policy.
const workflowHTTPMaxRedirects = 3

// workflowHTTPBodyLimit caps the response body recorded into run state.
const workflowHTTPBodyLimit = 64 * 1024

// workflowHTTPDefaultTimeout applies when the node leaves
// timeout_seconds unset (0).
const workflowHTTPDefaultTimeout = 30 * time.Second

// runHTTP is the HTTPFunc adapter: intranet-only egress. Only loopback /
// private / link-local targets are reachable — the deployment is an
// intranet product and this doubles as the exfiltration guard (a workflow
// cannot phone out to a public endpoint even if the host network could).
//
// ponytail: DNS is re-validated per redirect hop but NOT pinned between the
// check and the dial (classic rebinding TOCTOU window). Upgrade path:
// custom DialContext that validates the resolved IPs at connect time.
func (s *workflowService) runHTTP(ctx context.Context, req nodes.HTTPRequest) (*nodes.HTTPResult, error) {
	if err := validateIntranetURL(req.URL); err != nil {
		return nil, err
	}
	timeout := workflowHTTPDefaultTimeout
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	hopCount := 0
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			hopCount++
			if hopCount > workflowHTTPMaxRedirects {
				return fmt.Errorf("workflow HTTP: more than %d redirects", workflowHTTPMaxRedirects)
			}
			return validateIntranetURL(r.URL.String())
		},
	}

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	var bodyReader io.Reader
	if req.Body != "" && method != http.MethodGet && method != http.MethodHead {
		bodyReader = strings.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("workflow HTTP: bad request: %w", err)
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("workflow HTTP: call failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, workflowHTTPBodyLimit+1))
	if err != nil {
		return nil, fmt.Errorf("workflow HTTP: reading response: %w", err)
	}
	truncated := false
	if len(body) > workflowHTTPBodyLimit {
		body = body[:workflowHTTPBodyLimit]
		truncated = true
	}
	headers := map[string]string{}
	for k := range resp.Header {
		headers[k] = resp.Header.Get(k)
	}
	logger.Infof(ctx, "[workflow] HTTP %s %s -> %d (%dB%s)", method, req.URL, resp.StatusCode, len(body),
		map[bool]string{true: ", truncated", false: ""}[truncated])
	return &nodes.HTTPResult{StatusCode: resp.StatusCode, Body: string(body), Headers: headers}, nil
}

// validateIntranetURL accepts only http(s) URLs whose host resolves
// exclusively to loopback / private / link-local addresses.
func validateIntranetURL(raw string) error {
	normalized := raw
	if !strings.Contains(normalized, "://") {
		normalized = "http://" + normalized
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return fmt.Errorf("workflow HTTP: invalid URL: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("workflow HTTP: scheme %q not allowed (http/https only)", parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("workflow HTTP: URL has no hostname")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isIntranetIP(ip) {
			return fmt.Errorf("workflow HTTP: host %s is outside the intranet (private/loopback ranges only)", host)
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("workflow HTTP: cannot resolve host %q: %v", host, err)
	}
	for _, ip := range ips {
		if !isIntranetIP(ip) {
			return fmt.Errorf("workflow HTTP: host %q resolves to non-intranet address %s", host, ip)
		}
	}
	return nil
}

func isIntranetIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// workflowDataOpsRowCap / workflowDataOpsSQLMax bound what a single node can
// pull into run state.
const (
	workflowDataOpsRowCap = 1000
	workflowDataOpsSQLMax = 4096
	dataOpsPlaceholderRe  = `\$([A-Za-z_][A-Za-z0-9_]*)`
)

var dataOpsPlaceholder = regexp.MustCompile(dataOpsPlaceholderRe)

// runDataOps is the DataOpsFunc adapter: one in-memory DuckDB session per
// call (stateless, no cross-run leakage), SELECT-only and single-statement
// enforced through the platform's shared SQL validator, `$name` named
// placeholders rewritten to positional binds — values are never
// string-interpolated into the SQL text.
func (s *workflowService) runDataOps(ctx context.Context, req nodes.DataOpsRequest) (*nodes.DataOpsResult, error) {
	query := strings.TrimSpace(req.SQL)
	if query == "" {
		return nil, fmt.Errorf("workflow DataOps: empty sql")
	}
	if len(query) > workflowDataOpsSQLMax {
		return nil, fmt.Errorf("workflow DataOps: sql exceeds %d chars", workflowDataOpsSQLMax)
	}

	// Rewrite $name placeholders to positional ? binds in first-occurrence
	// order and collect the bound values.
	args := make([]any, 0, len(req.Args))
	seen := map[string]bool{}
	var bindErr error
	bound := dataOpsPlaceholder.ReplaceAllStringFunc(query, func(m string) string {
		name := strings.TrimPrefix(m, "$")
		val, ok := req.Args[name]
		if !ok {
			bindErr = fmt.Errorf("workflow DataOps: no value provided for placeholder $%s", name)
			return m
		}
		args = append(args, val)
		seen[name] = true
		return "?"
	})
	if bindErr != nil {
		return nil, bindErr
	}
	for name := range req.Args {
		if !seen[name] {
			return nil, fmt.Errorf("workflow DataOps: variable %q is not referenced by any $placeholder in sql", name)
		}
	}
	// Read-only + single-statement guard. The shared utils.ValidateSQL is
	// table-centric ("no valid table found" for SELECT 1) and so cannot
	// validate statements against an empty in-memory database; this mirrors
	// the prefix/scope guard internal/agent/tools/data_analysis.go applies
	// ahead of its own table-aware validation.
	if err := validateDataOpsStatement(bound); err != nil {
		return nil, fmt.Errorf("workflow DataOps: sql rejected: %w", err)
	}

	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("workflow DataOps: open duckdb: %w", err)
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, bound, args...)
	if err != nil {
		return nil, fmt.Errorf("workflow DataOps: query failed: %w", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("workflow DataOps: columns: %w", err)
	}
	out := &nodes.DataOpsResult{Columns: columns, Rows: []map[string]any{}}
	for rows.Next() {
		if len(out.Rows) >= workflowDataOpsRowCap {
			break
		}
		vals := make([]any, len(columns))
		for i := range vals {
			vals[i] = new(any)
		}
		if err := rows.Scan(vals...); err != nil {
			return nil, fmt.Errorf("workflow DataOps: scan: %w", err)
		}
		row := map[string]any{}
		for i, c := range columns {
			row[c] = normalizeSQLValue(vals[i])
		}
		out.Rows = append(out.Rows, row)
	}
	return out, rows.Err()
}

// normalizeSQLValue converts driver-native values ([]byte etc.) into plain
// JSON-friendly Go values for run state.
func normalizeSQLValue(v any) any {
	switch t := v.(type) {
	case *any:
		if t == nil {
			return nil
		}
		return normalizeSQLValue(*t)
	case []byte:
		return string(t)
	default:
		return v
	}
}

// validateDataOpsStatement enforces the DataOps read-only / single-statement
// contract on the already-placeholder-rewritten statement: one leading
// read-only verb, no embedded statement separator. (Same verb list as
// internal/agent/tools/data_analysis.go.)
func validateDataOpsStatement(sql string) error {
	body := strings.TrimRight(strings.TrimSpace(sql), ";")
	if body == "" {
		return fmt.Errorf("empty statement")
	}
	if strings.Contains(body, ";") {
		return fmt.Errorf("multiple statements are not allowed")
	}
	lower := strings.ToLower(body)
	for _, verb := range []string{"select", "show", "describe", "explain", "pragma"} {
		if strings.HasPrefix(lower, verb) {
			return nil
		}
	}
	return fmt.Errorf("only read-only queries (SELECT/SHOW/DESCRIBE/EXPLAIN/PRAGMA) are allowed")
}
