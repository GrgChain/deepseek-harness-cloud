package skills

import (
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
)

type Skill struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	ModelInvocable bool       `json:"model_invocable"`
	UserInvocable  bool       `json:"user_invocable"`
	Version        int64      `json:"version"`
	FileCount      int        `json:"file_count"`
	Size           int        `json:"size_bytes"`
	SourceURL      string     `json:"source_url"`
	SourceDigest   string     `json:"-"`
	SyncedAt       *time.Time `json:"synced_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	HasPrevious    bool       `json:"has_previous"`
	Content        string     `json:"content,omitempty"`
	Files          []File     `json:"files,omitempty"`
	envelope       security.Envelope
}

type File struct {
	Name string `json:"name"`
	Size int    `json:"size_bytes"`
}

type FileContent struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Version  int64  `json:"version"`
	Editable bool   `json:"editable"`
}

type List struct {
	Items []Skill `json:"items"`
	Total int     `json:"total"`
	Page  int     `json:"page"`
	Limit int     `json:"limit"`
}

type PreviewItem struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Files        []File   `json:"files"`
	Conflict     bool     `json:"conflict"`
	LocalChanged bool     `json:"local_changed"`
	ChangedFiles []string `json:"changed_files"`
}

type Preview struct {
	Token     string        `json:"token"`
	Items     []PreviewItem `json:"items"`
	SourceURL string        `json:"source_url"`
	Unchanged bool          `json:"unchanged"`
}
