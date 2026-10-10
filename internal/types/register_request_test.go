package types

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRegisterRequestUnmarshalJSON locks down the tolerant decoder for
// RegisterRequest. The public register endpoint was previously 400ing on
// first-user signups because TDesign's <t-form :data> + <t-select
// v-model> combo can serialise an unfilled number field as the empty
// string. The custom UnmarshalJSON below must accept every shape a
// reasonable frontend might emit.
func TestRegisterRequestUnmarshalJSON(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantTID uint64
		wantErr bool
	}{
		{
			name:    "canonical numeric",
			raw:     `{"username":"u","email":"u@e.com","password":"pw1234","tenant_id":42}`,
			wantTID: 42,
		},
		{
			name:    "string-encoded number (TDesign quirk)",
			raw:     `{"username":"u","email":"u@e.com","password":"pw1234","tenant_id":"42"}`,
			wantTID: 42,
		},
		{
			name:    "empty string (TDesign undefined→'' coercion)",
			raw:     `{"username":"u","email":"u@e.com","password":"pw1234","tenant_id":""}`,
			wantTID: 0,
		},
		{
			name:    "explicit null",
			raw:     `{"username":"u","email":"u@e.com","password":"pw1234","tenant_id":null}`,
			wantTID: 0,
		},
		{
			name:    "field omitted",
			raw:     `{"username":"u","email":"u@e.com","password":"pw1234"}`,
			wantTID: 0,
		},
		{
			name:    "zero value (treated as no department — server falls back to create_personal)",
			raw:     `{"username":"u","email":"u@e.com","password":"pw1234","tenant_id":0}`,
			wantTID: 0,
		},
		{
			name:    "invalid string is an error",
			raw:     `{"username":"u","email":"u@e.com","password":"pw1234","tenant_id":"abc"}`,
			wantErr: true,
		},
		{
			name:    "negative number is an error",
			raw:     `{"username":"u","email":"u@e.com","password":"pw1234","tenant_id":-1}`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got RegisterRequest
			err := json.Unmarshal([]byte(tc.raw), &got)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (decoded: %+v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.TenantID != tc.wantTID {
				t.Fatalf("TenantID: want %d, got %d", tc.wantTID, got.TenantID)
			}
			if got.Username != "u" || got.Email != "u@e.com" || got.Password != "pw1234" {
				t.Fatalf("scalar fields not preserved: %+v", got)
			}
		})
	}
}

// TestRegisterRequestRoundTrip sanity-checks that MarshalJSON on the
// decoded value doesn't surprise us (e.g. tenant_id 0 gets serialised
// with `omitempty` so it doesn't bloat the response payload).
func TestRegisterRequestRoundTrip(t *testing.T) {
	in := RegisterRequest{Username: "u", Email: "u@e.com", Password: "pw1234"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "tenant_id") {
		t.Fatalf("omitempty should drop zero tenant_id, got: %s", b)
	}
}