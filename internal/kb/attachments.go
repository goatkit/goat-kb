package kb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// maxAttachmentSize is the per-file upload limit (10 MB).
const maxAttachmentSize = 10 * 1024 * 1024

// blockedContentTypes prevents uploading files that could execute scripts
// when served inline (SVG can carry <script> tags).
var blockedContentTypes = map[string]bool{
	"text/html":                true,
	"application/xhtml+xml":    true,
	"text/javascript":          true,
	"application/javascript":   true,
	"application/x-javascript": true,
	"image/svg+xml":            true,
}

// attachmentRow is the lightweight metadata returned to the UI.
type attachmentRow struct {
	ID          int64  `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	SizeHuman   string `json:"size_human"`
}

// humanSize converts a byte count to a human-readable string.
func humanSize(n int64) string {
	switch {
	case n >= 1_048_576:
		return fmt.Sprintf("%.1f MB", float64(n)/1_048_576)
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// sanitiseFilename strips path components and dangerous characters.
func sanitiseFilename(name string) string {
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	if idx := strings.LastIndex(name, "\\"); idx >= 0 {
		name = name[idx+1:]
	}
	name = sanitiseText(name)
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}

// fetchAttachments retrieves all attachments for an article, scoped by org.
func (p *Plugin) fetchAttachments(ctx context.Context, orgID, articleID int64) []attachmentRow {
	rows, err := p.host.DBQuery(ctx,
		"SELECT id, filename, content_type, size FROM gk_kb_attachments WHERE article_id = ? AND org_id = ? ORDER BY created_at",
		articleID, orgID)
	if err != nil {
		return nil
	}
	result := make([]attachmentRow, 0, len(rows))
	for _, row := range rows {
		size := toInt64(row["size"])
		result = append(result, attachmentRow{
			ID:          toInt64(row["id"]),
			Filename:    toString(row["filename"]),
			ContentType: toString(row["content_type"]),
			Size:        size,
			SizeHuman:   humanSize(size),
		})
	}
	return result
}

// handleAttachmentUpload accepts a base64-encoded file, stores it via the
// HostAPI file-storage layer (local or cloud-backed), and creates a DB row.
//
// Args JSON: {"id": "articleID", "filename": "...", "content_type": "...", "data": "base64..."}
func (p *Plugin) handleAttachmentUpload(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if p.host == nil {
		return errorResponse(503, "host API not available")
	}

	var req struct {
		ID          string `json:"id"`
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		Data        string `json:"data"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return errorResponse(400, "invalid request: "+err.Error())
	}

    // If id wasn't in the JSON body, try URL path params (pasted images from
    // TipTap send id via the URL rather than the POST body).
    if req.ID == "" {
        raw := make(map[string]interface{})
        if err := json.Unmarshal(args, &raw); err == nil {
            if params, ok := raw["params"].(map[string]interface{}); ok {
                if pid, ok := params["id"]; ok {
                    if s, ok := pid.(string); ok {
                        req.ID = s
                    }
                }
            }
        }
    }

	rc := extractReqCtx(args)
	orgID := rc.OrgID
	if orgID == 0 {
		orgID = p.host.OrgID(ctx)
	}
	if orgID == 0 {
		orgID = 1
	}

	// Validate article ID.
	articleID, err := parseInt64(req.ID)
	if err != nil || articleID < 1 {
		return errorResponse(400, "invalid article id")
	}

	// Verify article exists and belongs to this org.
	artRows, err := p.host.DBQuery(ctx,
		"SELECT id FROM gk_kb_articles WHERE id = ? AND org_id = ?", articleID, orgID)
	if err != nil {
		return errorResponse(500, "check article: "+err.Error())
	}
	if len(artRows) == 0 {
		return errorResponse(404, "article not found")
	}

	filename := sanitiseFilename(req.Filename)
	if filename == "" {
		return errorResponse(400, "filename required")
	}

	contentType := strings.TrimSpace(strings.ToLower(req.ContentType))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if blockedContentTypes[contentType] {
		return errorResponse(400, "file type not allowed: "+contentType)
	}

	// Check base64 string length BEFORE decoding to prevent memory exhaustion.
	// Base64 inflates by ~33%, so check against 4/3 of the max size.
	if len(req.Data) > (maxAttachmentSize*4)/3 {
		return errorResponse(413, fmt.Sprintf("file too large (max %s)", humanSize(maxAttachmentSize)))
	}

	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		return errorResponse(400, "invalid file data: base64 decode failed")
	}
	if len(data) == 0 {
		return errorResponse(400, "file is empty")
	}
	if len(data) > maxAttachmentSize {
		return errorResponse(413, fmt.Sprintf("file too large: %s (max %s)", humanSize(int64(len(data))), humanSize(maxAttachmentSize)))
	}

	// Generate a server-side file key with unique timestamp to allow
	// same-named files on the same article.
	fileKey := fmt.Sprintf("kb/%d/%d/%d_%s", orgID, articleID, time.Now().UnixNano(), filename)

	// Store the file bytes via the HostAPI file-storage layer.
	err = p.host.StoreFile(ctx, fileKey, data, map[string]string{
		"filename":     filename,
		"content_type": contentType,
	})
	if err != nil {
		return errorResponse(500, "store file: "+err.Error())
	}

	// Insert the DB row.
	result, err := p.host.DBExec(ctx,
		"INSERT INTO gk_kb_attachments (org_id, article_id, file_key, filename, content_type, size) VALUES (?, ?, ?, ?, ?, ?)",
		orgID, articleID, fileKey, filename, contentType, len(data))
	if err != nil {
		_ = p.host.DeleteFile(ctx, fileKey) // best-effort cleanup
		return errorResponse(500, "insert attachment: "+err.Error())
	}

	attachID := toInt64(result)
	p.host.Log(ctx, "info", "kb: attachment uploaded", map[string]any{
		"org_id":        orgID,
		"user":          rc.Login,
		"article_id":    articleID,
		"attachment_id": attachID,
		"filename":      filename,
		"size":          len(data),
	})

	return json.Marshal(map[string]any{
		"status":       "uploaded",
		"id":           attachID,
		"filename":     filename,
		"content_type": contentType,
		"size":         len(data),
		"size_human":   humanSize(int64(len(data))),
	})
}

// handleAttachmentDelete removes a file from storage and deletes the DB row.
//
// Args JSON: {"aid": "attachmentID"}
func (p *Plugin) handleAttachmentDelete(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if p.host == nil {
		return errorResponse(503, "host API not available")
	}

	var req struct {
		AID string `json:"aid"`
		ID  string `json:"id"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return errorResponse(400, "invalid request: "+err.Error())
	}

	aidStr := req.AID
	if aidStr == "" {
		aidStr = req.ID
	}

	rc := extractReqCtx(args)
	orgID := rc.OrgID
	if orgID == 0 {
		orgID = p.host.OrgID(ctx)
	}
	if orgID == 0 {
		orgID = 1
	}

	attachID, err := parseInt64(aidStr)
	if err != nil || attachID < 1 {
		return errorResponse(400, "invalid attachment id")
	}

	rows, err := p.host.DBQuery(ctx,
		"SELECT file_key, filename FROM gk_kb_attachments WHERE id = ? AND org_id = ?", attachID, orgID)
	if err != nil {
		return errorResponse(500, "query attachment: "+err.Error())
	}
	if len(rows) == 0 {
		return errorResponse(404, "attachment not found")
	}

	fileKey := toString(rows[0]["file_key"])
	filename := toString(rows[0]["filename"])

	_ = p.host.DeleteFile(ctx, fileKey) // best-effort

	_, err = p.host.DBExec(ctx,
		"DELETE FROM gk_kb_attachments WHERE id = ? AND org_id = ?", attachID, orgID)
	if err != nil {
		return errorResponse(500, "delete attachment: "+err.Error())
	}

	p.host.Log(ctx, "info", "kb: attachment deleted", map[string]any{
		"org_id":        orgID,
		"user":          rc.Login,
		"attachment_id": attachID,
		"filename":      filename,
	})

	return json.Marshal(map[string]string{"status": "deleted"})
}

// handleAttachmentDownload retrieves a file and returns it as base64 in JSON.
// The client-side JS creates a Blob and triggers a download.
// Customer access requires the parent article to be published + public.
//
// Args JSON: {"aid": "attachmentID"}
func (p *Plugin) handleAttachmentDownload(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if p.host == nil {
		return errorResponse(503, "host API not available")
	}

	var req struct {
		AID string `json:"aid"`
		ID  string `json:"id"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return errorResponse(400, "invalid request: "+err.Error())
	}

	aidStr := req.AID
	if aidStr == "" {
		aidStr = req.ID
	}

	rc := extractReqCtx(args)
	orgID := rc.OrgID
	if orgID == 0 {
		orgID = p.host.OrgID(ctx)
	}
	if orgID == 0 {
		orgID = 1
	}

	attachID, err := parseInt64(aidStr)
	if err != nil || attachID < 1 {
		return errorResponse(400, "invalid attachment id")
	}

	// Join to articles for visibility check.
	rows, err := p.host.DBQuery(ctx,
		`SELECT a.file_key, a.filename, a.content_type,
		        art.visibility, art.status
		 FROM gk_kb_attachments a
		 JOIN gk_kb_articles art ON art.id = a.article_id
		 WHERE a.id = ? AND a.org_id = ?`, attachID, orgID)
	if err != nil {
		return errorResponse(500, "query attachment: "+err.Error())
	}
	if len(rows) == 0 {
		return errorResponse(404, "attachment not found")
	}

	row := rows[0]
	if !rc.canSeeVisibility(toString(row["visibility"])) || toString(row["status"]) != "published" {
		return errorResponse(404, "attachment not found")
	}

	data, _, err := p.host.GetFile(ctx, toString(row["file_key"]))
	if err != nil {
		return errorResponse(500, "retrieve file: "+err.Error())
	}

	return json.Marshal(map[string]string{
		"data":         base64.StdEncoding.EncodeToString(data),
		"filename":     toString(row["filename"]),
		"content_type": toString(row["content_type"]),
		"size_human":   humanSize(int64(len(data))),
	})
}

// deleteArticleAttachments removes all files and DB rows for an article.
// Called from handleAdminArticleDelete to prevent orphaned files in storage.
func (p *Plugin) deleteArticleAttachments(ctx context.Context, orgID, articleID int64) {
	rows, err := p.host.DBQuery(ctx,
		"SELECT file_key FROM gk_kb_attachments WHERE article_id = ? AND org_id = ?", articleID, orgID)
	if err != nil {
		return
	}
	for _, row := range rows {
		_ = p.host.DeleteFile(ctx, toString(row["file_key"]))
	}
	_, _ = p.host.DBExec(ctx,
		"DELETE FROM gk_kb_attachments WHERE article_id = ? AND org_id = ?", articleID, orgID)
}

// parseInt64 parses a string or numeric value from JSON args into int64.
func parseInt64(v string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(v, "%d", &n)
	return n, err
}
