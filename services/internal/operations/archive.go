package operations

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

// Backup keys are independent from the data-encryption keyring. HKDF derives
// an independent AES-GCM key for every random 256-bit archive salt. Authenticated
// sequence, frame type/length and a mandatory final digest reject reordering,
// truncation, substitution and concatenation. No plaintext metadata is emitted.
const archiveChunk = 64 << 10
const archiveMaximum int64 = 128 << 30

var errArchive = errors.New("backup integrity or format verification failed")
var archiveImageDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var databaseName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type BackupManifest struct {
	SchemaVersion   int       `json:"schema_version"`
	Kind            string    `json:"kind"`
	OrganizationID  string    `json:"organization_id"`
	CreatedAt       time.Time `json:"created_at"`
	Database        string    `json:"database"`
	InstanceID      string    `json:"instance_id,omitempty"`
	AssetID         string    `json:"asset_id,omitempty"`
	OwnerID         string    `json:"owner_id,omitempty"`
	Capacity        int64     `json:"capacity_bytes,omitempty"`
	AuditBatchID    string    `json:"audit_batch_id,omitempty"`
	AuditCount      int       `json:"audit_count,omitempty"`
	AuditCutoff     time.Time `json:"audit_cutoff,omitempty,omitzero"`
	BackupSetID     string    `json:"backup_set_id,omitempty"`
	TemplateVersion string    `json:"template_version,omitempty"`
	ImageDigest     string    `json:"image_digest,omitempty"`
}

func (m BackupManifest) valid() bool {
	if m.SchemaVersion != 1 || !uuid.MatchString(m.OrganizationID) || m.CreatedAt.IsZero() || !m.CreatedAt.Before(time.Now().Add(5*time.Minute)) {
		return false
	}
	if m.TemplateVersion != "" || m.ImageDigest != "" {
		if m.Kind != "storage" || !uuid.MatchString(m.TemplateVersion) || !archiveImageDigest.MatchString(m.ImageDigest) {
			return false
		}
	}
	if m.Kind == "backup-set" {
		return uuid.MatchString(m.BackupSetID) && databaseName.MatchString(m.Database) && m.InstanceID == "" && m.AssetID == "" && m.OwnerID == "" && m.Capacity == 0 && m.AuditBatchID == "" && m.AuditCount == 0 && m.AuditCutoff.IsZero()
	}
	if m.BackupSetID != "" {
		return false
	}
	if m.Kind == "audit" {
		return databaseName.MatchString(m.Database) && m.InstanceID == "" && m.AssetID == "" && m.OwnerID == "" && m.Capacity == 0 && uuid.MatchString(m.AuditBatchID) && m.AuditCount >= 1 && m.AuditCount <= 500 && !m.AuditCutoff.IsZero() && !m.AuditCutoff.After(m.CreatedAt.Add(-90*24*time.Hour))
	}
	if m.AuditBatchID != "" || m.AuditCount != 0 || !m.AuditCutoff.IsZero() {
		return false
	}
	if m.Kind == "postgres" {
		return databaseName.MatchString(m.Database) && m.InstanceID == "" && m.AssetID == "" && m.OwnerID == "" && m.Capacity == 0
	}
	return m.Kind == "storage" && m.Database == "" && uuid.MatchString(m.InstanceID) && uuid.MatchString(m.AssetID) && uuid.MatchString(m.OwnerID) && runtimeapproval.SupportedStorage(m.Capacity)
}

type ArchiveProof struct {
	SHA256      string `json:"sha256"`
	PlainSHA256 string `json:"plain_sha256"`
	Bytes       int64  `json:"bytes"`
}

func archiveAEAD(key, header []byte) (cipher.AEAD, error) {
	if len(key) != 32 || len(header) != 40 || string(header[:8]) != "HCBK0001" {
		return nil, errArchive
	}
	derived, err := hkdf.Key(sha256.New, key, header[8:], "harness-cloud-backup-v1", 32)
	if err != nil {
		return nil, errArchive
	}
	defer clear(derived)
	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, errArchive
	}
	return cipher.NewGCM(block)
}
func frameAAD(header, frame []byte, sequence uint64) ([]byte, []byte) {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], sequence)
	aad := make([]byte, 0, 53)
	aad = append(aad, header...)
	aad = append(aad, frame...)
	aad = binary.BigEndian.AppendUint64(aad, sequence)
	return nonce, aad
}
func SealArchive(w io.Writer, r io.Reader, key []byte, m BackupManifest) (ArchiveProof, error) {
	if !m.valid() {
		return ArchiveProof{}, ErrInput
	}
	header := make([]byte, 40)
	copy(header, "HCBK0001")
	if _, err := rand.Read(header[8:]); err != nil {
		return ArchiveProof{}, err
	}
	aead, err := archiveAEAD(key, header)
	if err != nil {
		return ArchiveProof{}, err
	}
	archiveHash, plainHash := sha256.New(), sha256.New()
	output := io.MultiWriter(w, archiveHash)
	if _, err = output.Write(header); err != nil {
		return ArchiveProof{}, err
	}
	sequence := uint64(0)
	writeFrame := func(kind byte, data []byte) error {
		frame := make([]byte, 5)
		binary.BigEndian.PutUint32(frame, uint32(len(data)))
		frame[4] = kind
		nonce, aad := frameAAD(header, frame, sequence)
		sequence++
		sealed := aead.Seal(nil, nonce, data, aad)
		if _, err := output.Write(frame); err != nil {
			return err
		}
		_, err := output.Write(sealed)
		return err
	}
	metadata, err := json.Marshal(m)
	if err != nil {
		return ArchiveProof{}, err
	}
	if err = writeFrame(1, metadata); err != nil {
		return ArchiveProof{}, err
	}
	buffer := make([]byte, archiveChunk)
	defer clear(buffer)
	var total int64
	for {
		n, readErr := io.ReadFull(r, buffer)
		if n > 0 {
			total += int64(n)
			if total > archiveMaximum {
				return ArchiveProof{}, errArchive
			}
			_, _ = plainHash.Write(buffer[:n])
			if err = writeFrame(2, buffer[:n]); err != nil {
				return ArchiveProof{}, err
			}
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return ArchiveProof{}, readErr
		}
	}
	footer := binary.BigEndian.AppendUint64(nil, uint64(total))
	footer = append(footer, plainHash.Sum(nil)...)
	if err = writeFrame(3, footer); err != nil {
		return ArchiveProof{}, err
	}
	return ArchiveProof{hex.EncodeToString(archiveHash.Sum(nil)), hex.EncodeToString(plainHash.Sum(nil)), total}, nil
}

// OpenArchive writes only to private staging. Callers must not publish or act on
// the staged content unless this function has authenticated the complete stream.
func OpenArchive(w io.Writer, r io.Reader, key []byte) (BackupManifest, ArchiveProof, error) {
	bad := func() (BackupManifest, ArchiveProof, error) { return BackupManifest{}, ArchiveProof{}, errArchive }
	archiveHash, plainHash := sha256.New(), sha256.New()
	input := io.TeeReader(r, archiveHash)
	header := make([]byte, 40)
	if _, err := io.ReadFull(input, header); err != nil {
		return bad()
	}
	aead, err := archiveAEAD(key, header)
	if err != nil {
		return bad()
	}
	var m BackupManifest
	var total int64
	for sequence := uint64(0); sequence < uint64(archiveMaximum/archiveChunk)+3; sequence++ {
		frame := make([]byte, 5)
		if _, err := io.ReadFull(input, frame); err != nil {
			return bad()
		}
		n := binary.BigEndian.Uint32(frame)
		kind := frame[4]
		if n > archiveChunk || (sequence == 0 && kind != 1) || (sequence > 0 && kind != 2 && kind != 3) || (kind == 3 && n != 40) {
			return bad()
		}
		sealed := make([]byte, int(n)+aead.Overhead())
		if _, err := io.ReadFull(input, sealed); err != nil {
			return bad()
		}
		nonce, aad := frameAAD(header, frame, sequence)
		plain, err := aead.Open(nil, nonce, sealed, aad)
		if err != nil {
			return bad()
		}
		switch kind {
		case 1:
			err = webapi.StrictUnmarshal(plain, &m)
			clear(plain)
			if err != nil || !m.valid() {
				return bad()
			}
		case 2:
			total += int64(len(plain))
			if len(plain) == 0 || total > archiveMaximum {
				clear(plain)
				return bad()
			}
			_, _ = plainHash.Write(plain)
			_, err = w.Write(plain)
			clear(plain)
			if err != nil {
				return BackupManifest{}, ArchiveProof{}, err
			}
		case 3:
			if binary.BigEndian.Uint64(plain[:8]) != uint64(total) || !bytes.Equal(plain[8:], plainHash.Sum(nil)) {
				clear(plain)
				return bad()
			}
			clear(plain)
			var extra [1]byte
			if n, err := input.Read(extra[:]); n != 0 || err != io.EOF {
				return bad()
			}
			return m, ArchiveProof{hex.EncodeToString(archiveHash.Sum(nil)), hex.EncodeToString(plainHash.Sum(nil)), total}, nil
		}
	}
	return bad()
}
