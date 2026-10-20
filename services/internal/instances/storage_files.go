package instances

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

const (
	StorageDirectoryPageSize = 100
	StoragePreviewMaxBytes   = 512 << 10
	StorageDownloadMaxBytes  = 64 << 20
)

var (
	ErrStorageBrowseBusy         = errors.New("storage browse conflicts with another operation")
	ErrStorageBrowseUnavailable  = errors.New("storage browse is unavailable")
	ErrStorageFileUnavailable    = errors.New("storage file is unavailable")
	ErrStoragePreviewUnavailable = errors.New("storage file cannot be previewed as text")
	ErrStorageDirectoryTooLarge  = errors.New("storage directory has too many entries")
	ErrStorageFileTooLarge       = errors.New("storage file exceeds the read limit")
)

type StorageBrowseInput struct {
	Action string
	Root   string
	Path   string
	After  string
}

type StorageDirectoryEntry struct {
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	Size       int64     `json:"size_bytes"`
	ModifiedAt time.Time `json:"modified_at"`
}

type StorageDirectory struct {
	Root      string                  `json:"root"`
	Path      string                  `json:"path"`
	Entries   []StorageDirectoryEntry `json:"entries"`
	NextAfter string                  `json:"next_after,omitempty"`
}

type StorageFile struct {
	Name    string
	Content []byte
}

const storageBrowseEnvelopeVersion byte = 1

type StorageBrowseKey struct{ private *ecdh.PrivateKey }

func NewStorageBrowseKey() (*StorageBrowseKey, []byte, error) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return &StorageBrowseKey{private: private}, private.PublicKey().Bytes(), nil
}

func SealStorageBrowseContent(requestID string, recipientPublic, content []byte) ([]byte, error) {
	if requestID == "" || len(recipientPublic) != 32 {
		return nil, errors.New("invalid storage response key")
	}
	recipient, err := ecdh.X25519().NewPublicKey(recipientPublic)
	if err != nil {
		return nil, err
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := ephemeral.ECDH(recipient)
	if err != nil {
		return nil, err
	}
	defer clear(shared)
	aead, err := storageBrowseAEAD(shared, requestID)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := aead.Seal(nil, nonce, content, storageBrowseAAD(requestID))
	envelope := make([]byte, 1, 1+32+len(nonce)+len(sealed))
	envelope[0] = storageBrowseEnvelopeVersion
	envelope = append(envelope, ephemeral.PublicKey().Bytes()...)
	envelope = append(envelope, nonce...)
	envelope = append(envelope, sealed...)
	return envelope, nil
}

func (k *StorageBrowseKey) Open(requestID string, envelope []byte) ([]byte, error) {
	if k == nil || k.private == nil || requestID == "" || len(envelope) < 1+32+12+16 || envelope[0] != storageBrowseEnvelopeVersion {
		return nil, errors.New("invalid storage response envelope")
	}
	ephermal, err := ecdh.X25519().NewPublicKey(envelope[1 : 1+32])
	if err != nil {
		return nil, err
	}
	shared, err := k.private.ECDH(ephermal)
	if err != nil {
		return nil, err
	}
	defer clear(shared)
	aead, err := storageBrowseAEAD(shared, requestID)
	if err != nil {
		return nil, err
	}
	start := 1 + 32
	end := start + aead.NonceSize()
	if len(envelope) < end+aead.Overhead() {
		return nil, errors.New("invalid storage response envelope")
	}
	return aead.Open(nil, envelope[start:end], envelope[end:], storageBrowseAAD(requestID))
}

func storageBrowseAEAD(shared []byte, requestID string) (cipher.AEAD, error) {
	material := append([]byte("DSH Cloud storage browse v1\x00"), []byte(requestID)...)
	material = append(material, 0)
	material = append(material, shared...)
	key := sha256.Sum256(material)
	clear(material)
	block, err := aes.NewCipher(key[:])
	clear(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func storageBrowseAAD(requestID string) []byte {
	return []byte("DSH Cloud storage browse response v1:" + requestID)
}

func ValidateStorageBrowse(input StorageBrowseInput) error {
	if input.Root != "workspace" && input.Root != "dsh" {
		return invalidQuery()
	}
	if input.Action != "LIST" && input.Action != "PREVIEW" && input.Action != "DOWNLOAD" {
		return invalidQuery()
	}
	if input.Action != "LIST" && input.After != "" {
		return invalidQuery()
	}
	for _, value := range []string{input.Path, input.After} {
		if len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\\x00") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
			return invalidQuery()
		}
		if value == "" {
			continue
		}
		for _, part := range strings.Split(value, "/") {
			if part == "" || part == "." || part == ".." || len(part) > 255 {
				return invalidQuery()
			}
		}
	}
	if len(input.After) > 255 {
		return invalidQuery()
	}
	if strings.Contains(input.After, "/") {
		return invalidQuery()
	}
	if input.Action != "LIST" && input.Path == "" {
		return invalidQuery()
	}
	return nil
}

func (s *QueryService) StorageDirectory(ctx context.Context, p identity.Principal, id, root, path, after string) (StorageDirectory, error) {
	input := StorageBrowseInput{Action: "LIST", Root: root, Path: path, After: after}
	var out StorageDirectory
	if !idPattern.MatchString(id) {
		return out, invalidQuery()
	}
	if err := ValidateStorageBrowse(input); err != nil {
		return out, err
	}
	raw, _, _, err := s.storageFileRequest(ctx, p, id, input)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out); err != nil || out.Root != root || out.Path != path || len(out.Entries) > StorageDirectoryPageSize {
		return StorageDirectory{}, errors.New("storage directory response is invalid")
	}
	for _, entry := range out.Entries {
		if entry.Name == "" || len(entry.Name) > 255 || !utf8.ValidString(entry.Name) || strings.ContainsAny(entry.Name, "/\\\x00") || entry.Size < 0 || entry.ModifiedAt.IsZero() || (entry.Type != "DIRECTORY" && entry.Type != "FILE" && entry.Type != "LINK" && entry.Type != "OTHER") {
			return StorageDirectory{}, errors.New("storage directory entry is invalid")
		}
	}
	if out.NextAfter != "" && (len(out.Entries) == 0 || len(out.NextAfter) > 255 || out.NextAfter != out.Entries[len(out.Entries)-1].Name) {
		return StorageDirectory{}, errors.New("storage directory cursor is invalid")
	}
	return out, nil
}

func (s *QueryService) StorageFile(ctx context.Context, p identity.Principal, id, root, path, action string) (StorageFile, error) {
	input := StorageBrowseInput{Action: strings.ToUpper(action), Root: root, Path: path}
	var out StorageFile
	if !idPattern.MatchString(id) {
		return out, invalidQuery()
	}
	if err := ValidateStorageBrowse(input); err != nil {
		return out, err
	}
	_, content, name, err := s.storageFileRequest(ctx, p, id, input)
	if err != nil {
		return out, err
	}
	if name == "" || len(content) > StorageDownloadMaxBytes || input.Action == "PREVIEW" && (len(content) > StoragePreviewMaxBytes || !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0) {
		return out, errors.New("storage file response is invalid")
	}
	return StorageFile{Name: name, Content: content}, nil
}

func (s *QueryService) storageFileRequest(ctx context.Context, p identity.Principal, assetID string, input StorageBrowseInput) ([]byte, []byte, string, error) {
	tx, _, err := s.begin(ctx, p, "instances:read")
	if err != nil {
		return nil, nil, "", err
	}
	defer tx.Rollback(context.Background())
	var instanceID string
	err = tx.QueryRow(ctx, `SELECT i.id::text FROM storage_assets a JOIN instances i ON i.id=a.instance_id AND i.storage_asset_id=a.id WHERE a.id=$1 AND a.org_id=$2 AND a.owner_id=$3`, assetID, p.OrganizationID, p.UserID).Scan(&instanceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, "", queryNotFound()
	}
	if err != nil {
		return nil, nil, "", err
	}
	requestID := webapi.ID()
	var resultKey *StorageBrowseKey
	var resultPublicKey []byte
	if input.Action != "LIST" {
		resultKey, resultPublicKey, err = NewStorageBrowseKey()
		if err != nil {
			return nil, nil, "", err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM storage_browse_requests WHERE asset_id=$1 AND expires_at<=clock_timestamp()`, assetID); err != nil {
		return nil, nil, "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO storage_browse_requests(id,org_id,owner_id,instance_id,asset_id,action,root,path,after_name,result_public_key,status,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'QUEUED',clock_timestamp()+interval '2 minutes')`, requestID, p.OrganizationID, p.UserID, instanceID, assetID, input.Action, input.Root, input.Path, input.After, resultPublicKey)
	if err != nil {
		var database *pgconn.PgError
		if errors.As(err, &database) && database.Code == "23505" {
			return nil, nil, "", webapi.Fail(409, "STORAGE_BUSY", "此数据盘正在处理其他查看请求")
		}
		return nil, nil, "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, nil, "", err
	}
	return s.waitStorageFileRequest(ctx, p, requestID, input.Action, resultKey)
}

func (s *QueryService) waitStorageFileRequest(ctx context.Context, p identity.Principal, id, action string, resultKey *StorageBrowseKey) ([]byte, []byte, string, error) {
	wait, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var status string
		var code *string
		var listing, content []byte
		var name *string
		err := s.pool.QueryRow(wait, `SELECT status,listing,content,file_name,error_code FROM storage_browse_requests WHERE id=$1 AND org_id=$2 AND owner_id=$3`, id, p.OrganizationID, p.UserID).Scan(&status, &listing, &content, &name, &code)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, "", webapi.Fail(503, "STORAGE_UNAVAILABLE", "数据读取请求已过期，请重试")
		}
		if err != nil {
			return nil, nil, "", err
		}
		errorCode := ""
		if code != nil {
			errorCode = *code
		}
		switch status {
		case "SUCCEEDED":
			deleteCtx, deleteCancel := context.WithTimeout(context.Background(), time.Second)
			_, _ = s.pool.Exec(deleteCtx, `DELETE FROM storage_browse_requests WHERE id=$1 AND org_id=$2 AND owner_id=$3`, id, p.OrganizationID, p.UserID)
			deleteCancel()
			if action == "LIST" {
				return listing, nil, "", nil
			}
			if name == nil {
				return nil, nil, "", errors.New("storage file name is missing")
			}
			content, err = resultKey.Open(id, content)
			if err != nil {
				return nil, nil, "", webapi.Fail(503, "STORAGE_UNAVAILABLE", "数据目录暂不可用，请稍后重试")
			}
			return nil, content, *name, nil
		case "FAILED":
			deleteCtx, deleteCancel := context.WithTimeout(context.Background(), time.Second)
			_, _ = s.pool.Exec(deleteCtx, `DELETE FROM storage_browse_requests WHERE id=$1 AND org_id=$2 AND owner_id=$3`, id, p.OrganizationID, p.UserID)
			deleteCancel()
			switch errorCode {
			case "STORAGE_BUSY":
				return nil, nil, "", webapi.Fail(409, "STORAGE_BUSY", "工作空间有待处理操作，请稍后重试")
			case "FILE_TOO_LARGE":
				return nil, nil, "", webapi.Fail(413, errorCode, "文件超过可下载大小限制")
			case "DIRECTORY_TOO_LARGE":
				return nil, nil, "", webapi.Fail(413, errorCode, "此目录内容过多，暂不能完整浏览")
			case "PREVIEW_UNAVAILABLE":
				return nil, nil, "", webapi.Fail(415, errorCode, "此文件无法作为文本预览，请下载后查看")
			case "FILE_UNAVAILABLE":
				return nil, nil, "", webapi.Fail(404, errorCode, "文件不存在或无法读取")
			default:
				return nil, nil, "", webapi.Fail(503, "STORAGE_UNAVAILABLE", "数据目录暂不可用，请稍后重试")
			}
		}
		select {
		case <-wait.Done():
			cleanup, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
			_, cleanupErr := s.pool.Exec(cleanup, `DELETE FROM storage_browse_requests WHERE id=$1 AND org_id=$2 AND owner_id=$3`, id, p.OrganizationID, p.UserID)
			cleanupCancel()
			if cleanupErr != nil {
				return nil, nil, "", cleanupErr
			}
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil, nil, "", context.Canceled
			}
			return nil, nil, "", webapi.Fail(504, "STORAGE_TIMEOUT", "数据读取超时，请稍后重试")
		case <-ticker.C:
		}
	}
}

func storageBrowseParameters(raw string, action string) (StorageBrowseInput, error) {
	allowed := []string{"root", "path"}
	if action == "LIST" {
		allowed = append(allowed, "after")
	}
	values, err := parameters(raw, allowed...)
	if err != nil {
		return StorageBrowseInput{}, err
	}
	input := StorageBrowseInput{Action: action, Root: values.Get("root"), Path: values.Get("path"), After: values.Get("after")}
	if err := ValidateStorageBrowse(input); err != nil {
		return StorageBrowseInput{}, err
	}
	return input, nil
}

func storageFileParameters(raw string) (StorageBrowseInput, error) {
	values, err := parameters(raw, "root", "path", "mode")
	if err != nil {
		return StorageBrowseInput{}, err
	}
	mode := strings.ToUpper(values.Get("mode"))
	input := StorageBrowseInput{Action: mode, Root: values.Get("root"), Path: values.Get("path")}
	if mode != "PREVIEW" && mode != "DOWNLOAD" || ValidateStorageBrowse(input) != nil {
		return StorageBrowseInput{}, invalidQuery()
	}
	return input, nil
}
