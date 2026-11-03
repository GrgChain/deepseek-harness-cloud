package skills

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

func editableText(data []byte) bool {
	return utf8.Valid(data) && !bytes.ContainsRune(data, 0) && strings.HasPrefix(http.DetectContentType(data), "text/")
}

func (s *Service) ReadFile(ctx context.Context, p identity.Principal, id, filename string) (FileContent, error) {
	tx, err := s.begin(ctx, p, false)
	if err != nil {
		return FileContent{}, err
	}
	defer tx.Rollback(context.Background())
	current, err := load(ctx, tx, p, id)
	if err != nil {
		return FileContent{}, err
	}
	if !safePath(filename) {
		return FileContent{}, invalid("文件路径无效")
	}
	bundle, err := s.open(p, current)
	if err != nil {
		return FileContent{}, err
	}
	data, exists := bundle.Files[filename]
	if !exists {
		return FileContent{}, missing()
	}
	out := FileContent{Path: filename, Version: current.Version, Editable: editableText(data)}
	if out.Editable {
		out.Content = string(data)
	}
	return out, tx.Commit(ctx)
}

func (s *Service) EditFile(ctx context.Context, p identity.Principal, id string, version int64, filename, content, trace string) (Skill, error) {
	return s.edit(ctx, p, id, version, filename, content, nil, false, trace)
}

func (s *Service) CreateFile(ctx context.Context, p identity.Principal, id string, version int64, filename, content, trace string) (Skill, error) {
	return s.edit(ctx, p, id, version, filename, content, nil, true, trace)
}
