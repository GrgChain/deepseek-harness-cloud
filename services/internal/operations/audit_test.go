package operations

import (
	"bytes"
	"testing"
	"time"
)

func TestAuditArchiveManifestKeepsRetentionAndRestoreKindsSeparate(t *testing.T) {
	now := time.Now().UTC()
	manifest := BackupManifest{SchemaVersion: 1, Kind: "audit", OrganizationID: "00000000-0000-4000-8000-000000000001", CreatedAt: now, Database: "hc_test_audit", AuditBatchID: "00000000-0000-4000-8000-000000000002", AuditCount: 1, AuditCutoff: now.Add(-90 * 24 * time.Hour)}
	var out bytes.Buffer
	if _, err := SealArchive(&out, bytes.NewBufferString("controlled audit"), bytes.Repeat([]byte{21}, 32), manifest); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*BackupManifest){
		func(m *BackupManifest) { m.AuditCutoff = now.Add(-89 * 24 * time.Hour) },
		func(m *BackupManifest) { m.AuditCount = 0 },
		func(m *BackupManifest) { m.AuditCount = 501 },
		func(m *BackupManifest) { m.AuditBatchID = "unbound" },
		func(m *BackupManifest) { m.Kind = "postgres" },
		func(m *BackupManifest) { m.Kind = "storage" },
	} {
		bad := manifest
		change(&bad)
		if _, err := SealArchive(&bytes.Buffer{}, bytes.NewReader(nil), bytes.Repeat([]byte{21}, 32), bad); err == nil {
			t.Fatal("invalid audit retention or confused restore kind accepted")
		}
	}
}
