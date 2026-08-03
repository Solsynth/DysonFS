package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"src.solsynth.dev/sosys/filesystem/internal/service"
)

const (
	reanalysisDefaultLimit = 100
	reanalysisMaxLimit     = 1000
)

// parseReanalysisLimit reads the limit from the query (or form) value,
// defaulting to 100 and capping at 1000 so a single scan or batch never walks
// an unbounded row set.
func parseReanalysisLimit(c *gin.Context) int {
	raw := c.Query("limit")
	if raw == "" {
		raw = c.PostForm("limit")
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return reanalysisDefaultLimit
	}
	if limit > reanalysisMaxLimit {
		return reanalysisMaxLimit
	}
	return limit
}

// filterReanalysisKind narrows candidates to one media kind ("image" or
// "video"); empty or "all" keeps everything.
func filterReanalysisKind(kind string, candidates []service.ReanalysisCandidate) []service.ReanalysisCandidate {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" || kind == "all" {
		return candidates
	}
	filtered := make([]service.ReanalysisCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Kind == kind {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

// listReanalysisCandidates scans existing files for missing or stale source
// metadata and derivatives, returning the files that a reanalysis run would
// repair, without touching anything.
//
//	GET /api/admin/reanalysis/candidates?limit=100&kind=image
func listReanalysisCandidates(c *gin.Context, files *service.FileService) {
	if !requireStorageAdminPermission(c, files) {
		return
	}
	candidates, err := files.ListReanalysisCandidates(c.Request.Context(), parseReanalysisLimit(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	candidates = filterReanalysisKind(c.Query("kind"), candidates)
	if candidates == nil {
		candidates = []service.ReanalysisCandidate{}
	}
	c.JSON(http.StatusOK, candidates)
}

// runReanalysis scans for files that need reanalysis and repairs them in one
// pass: each candidate is re-downloaded from its pool, its source metadata is
// recomputed, and missing image compression / video thumbnail derivatives are
// regenerated.
//
//	POST /api/admin/reanalysis/run
//	{ "limit": 100, "kind": "image" }
func runReanalysis(c *gin.Context, files *service.FileService) {
	if !requireStorageAdminPermission(c, files) {
		return
	}
	var req struct {
		Limit int    `json:"limit"`
		Kind  string `json:"kind"`
	}
	// The body is optional; a bare POST runs the default scan.
	_ = c.ShouldBindJSON(&req)
	limit := req.Limit
	if limit <= 0 {
		limit = parseReanalysisLimit(c)
	}
	if limit > reanalysisMaxLimit {
		limit = reanalysisMaxLimit
	}
	candidates, err := files.ListReanalysisCandidates(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	candidates = filterReanalysisKind(req.Kind, candidates)
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.FileID)
	}
	result, err := files.ReanalyzeFiles(c.Request.Context(), ids)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// reanalyzeFiles repairs exactly the file IDs in the request, whether or not
// the scan would have flagged them.
//
//	POST /api/admin/reanalysis/files
//	{ "file_ids": ["01J...", "01K..."] }
func reanalyzeFiles(c *gin.Context, files *service.FileService) {
	if !requireStorageAdminPermission(c, files) {
		return
	}
	var req struct {
		FileIDs []string `json:"file_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.FileIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_ids must not be empty"})
		return
	}
	result, err := files.ReanalyzeFiles(c.Request.Context(), req.FileIDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}
