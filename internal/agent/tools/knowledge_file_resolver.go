package tools

// Package tools — knowledge_file_resolver.
//
// Shared helper that turns a Knowledge row's FilePath into the correct
// FileService implementation. Used by every tool that needs to read a
// KB document's underlying source blob (data_analysis, kb_get_file,
// …). Centralising this avoids two copies of the provider / backend
// / tenant resolution + fallback policy drifting apart as storage
// backends are added.
//
// Resolution order (matches the legacy data_analysis.resolveFileServiceForKnowledge
// behaviour byte-for-byte so the new kb_get_file tool inherits the
// same cross-tenant / shared-KB behaviour data_analysis depended on):
//
//  1. Pull KB row to learn its provider + storage backend ID
//  2. Resolve the tenant from ctx (or via tenantService.GetTenantByID)
//  3. storageResolver.ResolveFileService(tenant, backendID, provider, localBaseDir)
//  4. Fall back to legacy NewFileServiceFromStorageConfig(provider, storageConfig, baseDir)
//  5. Fall back to the injected default FileService on any failure

import (
	"context"
	"strings"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ResolveFileServiceForKnowledge picks the provider-specific FileService for
// the source blob behind a Knowledge row. Falls back to fallback when
// provider / tenant / storage config cannot be resolved. Centralises the
// fallback policy shared by data_analysis and kb_get_file.
//
//   - logTag is the bracketed tool prefix in log lines, e.g.
//     "[Tool][KBGetFile][storage]". It identifies which caller is logging
//     so storage-backend failures stay filterable per tool.
//   - sessionID is the agent session identifier written into each log line;
//     pass the empty string when the caller has no session context.
//   - localBaseDir is the LOCAL_STORAGE_BASE_DIR value captured at
//     construction time by the caller. Re-reading os.Getenv here would
//     produce an empty string when the variable was set programmatically
//     before startup, causing the local FileService to fall back to the
//     /data/files default and miss the actual files (the bug #1040 fixed
//     for data_analysis).
//   - fallback MUST be non-nil. Every code path that cannot pick a
//     provider-specific service returns this; tools that pass nil will
//     panic on first use, which is the desired loud failure.
func ResolveFileServiceForKnowledge(
	ctx context.Context,
	knowledge *types.Knowledge,
	sessionID string,
	logTag string,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	tenantService interfaces.TenantService,
	storageResolver interfaces.StorageBackendResolver,
	fallback interfaces.FileService,
	localBaseDir string,
) interfaces.FileService {
	if knowledge == nil {
		logger.Warnf(ctx, "%s fallback default: session_id=%s reason=knowledge_nil", logTag, sessionID)
		return fallback
	}

	kbID := strings.TrimSpace(knowledge.KnowledgeBaseID)
	var kb *types.KnowledgeBase
	if knowledgeBaseService != nil && kbID != "" {
		var err error
		kb, err = knowledgeBaseService.GetKnowledgeBaseByID(ctx, kbID)
		if err != nil {
			logger.Warnf(ctx, "%s get kb failed, fallback default: session_id=%s knowledge_id=%s kb_id=%s err=%v",
				logTag, sessionID, knowledge.ID, kbID, err)
			return fallback
		}
	}
	if kb == nil && kbID != "" {
		logger.Infof(ctx, "%s kb not found, fallback default: session_id=%s knowledge_id=%s kb_id=%s",
			logTag, sessionID, knowledge.ID, kbID)
		return fallback
	}

	provider := ""
	backendID, _, _ := types.ParseStorageBackendPath(knowledge.FilePath)
	if kb != nil {
		provider = kb.GetStorageProvider()
		if backendID == "" && kb.StorageBackendID != nil {
			backendID = strings.TrimSpace(*kb.StorageBackendID)
		}
	}
	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if tenant == nil {
		tenantID := uint64(0)
		if tid, ok := ctx.Value(types.TenantIDContextKey).(uint64); ok {
			tenantID = tid
		}
		if tenantID == 0 && kb != nil {
			tenantID = knowledge.TenantID
		}
		if tenantID > 0 && tenantService != nil {
			resolvedTenant, err := tenantService.GetTenantByID(ctx, tenantID)
			if err != nil {
				logger.Warnf(ctx, "%s get tenant failed: session_id=%s knowledge_id=%s kb_id=%s tenant_id=%d err=%v",
					logTag, sessionID, knowledge.ID, kbID, tenantID, err)
			} else if resolvedTenant != nil {
				tenant = resolvedTenant
				logger.Infof(ctx, "%s resolved tenant from service: session_id=%s knowledge_id=%s kb_id=%s tenant_id=%d",
					logTag, sessionID, knowledge.ID, kbID, tenantID)
			}
		}
	}
	if provider == "" && tenant != nil && tenant.StorageEngineConfig != nil {
		provider = strings.ToLower(strings.TrimSpace(tenant.StorageEngineConfig.DefaultProvider))
	}
	if storageResolver != nil && tenant != nil && (backendID != "" || provider != "") {
		resolvedSvc, resolvedProvider, err := storageResolver.ResolveFileService(
			ctx, tenant, backendID, provider, localBaseDir,
		)
		if err == nil {
			logger.Infof(ctx, "%s resolved storage backend: session_id=%s knowledge_id=%s kb_id=%s backend_id=%s provider=%s",
				logTag, sessionID, knowledge.ID, kbID, backendID, resolvedProvider)
			return resolvedSvc
		}
		logger.Warnf(ctx, "%s resolve storage backend failed, trying legacy config: session_id=%s knowledge_id=%s kb_id=%s backend_id=%s provider=%s err=%v",
			logTag, sessionID, knowledge.ID, kbID, backendID, provider, err)
	}

	if provider == "" || tenant == nil || tenant.StorageEngineConfig == nil {
		hasTenantStorageConfig := tenant != nil && tenant.StorageEngineConfig != nil
		logger.Infof(ctx, "%s fallback default: session_id=%s knowledge_id=%s kb_id=%s provider=%q tenant_cfg=%t",
			logTag, sessionID, knowledge.ID, kbID, provider, hasTenantStorageConfig)
		return fallback
	}

	storageConfig := tenant.StorageEngineConfig
	// Use the localBaseDir captured at construction time rather than
	// re-reading LOCAL_STORAGE_BASE_DIR from os.Getenv here. Reading the
	// env var at request-handling time can produce an empty string (or
	// the wrong value) when the variable was set programmatically before
	// startup or is absent from the process environment of the DI-
	// constructed sub-component, causing the newly created local
	// FileService to use the /data/files fallback instead of the
	// configured path and therefore fail to locate files (#1040).
	baseDir := localBaseDir

	resolvedSvc, resolvedProvider, err := filesvc.NewFileServiceFromStorageConfig(provider, storageConfig, baseDir)
	if err != nil {
		logger.Warnf(ctx, "%s create file service failed, fallback default: session_id=%s knowledge_id=%s kb_id=%s provider=%s err=%v",
			logTag, sessionID, knowledge.ID, kbID, provider, err)
		return fallback
	}

	logger.Infof(ctx, "%s resolved file service: session_id=%s knowledge_id=%s kb_id=%s provider=%s",
		logTag, sessionID, knowledge.ID, kbID, resolvedProvider)
	return resolvedSvc
}
