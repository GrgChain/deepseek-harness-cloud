package instances

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

// An explicit model application may select the approved adapter release. This
// never happens merely because an administrator changed organization defaults.
func modelRuntime(ctx context.Context, tx pgx.Tx, snapshot *Snapshot, apply bool) error {
	if snapshot.Spec.Storage != 10<<30 {
		var supported bool
		if err := tx.QueryRow(ctx, `SELECT runtime_manifest->>'storage_adapter'=$3 FROM approved_templates WHERE id=$1 AND version=$2`, snapshot.TemplateID, snapshot.TemplateVersion, runtimeapproval.StorageAdapter).Scan(&supported); err != nil {
			return webapi.Fail(422, "CONFIGURATION_INVALID", "运行模板不支持此存储容量")
		}
		if !supported || !runtimeapproval.SupportedStorage(snapshot.Spec.Storage) {
			return webapi.Fail(422, "CONFIGURATION_INVALID", "运行模板不支持此存储容量")
		}
	}
	if snapshot.Provider != "platform-models" {
		return nil
	}
	var raw []byte
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT runtime_manifest,enabled FROM approved_templates WHERE id=$1 AND version=$2`, snapshot.TemplateID, snapshot.TemplateVersion).Scan(&raw, &enabled)
	if err != nil {
		return err
	}
	manifest, err := runtimeapproval.Parse(raw)
	if err == nil && manifest.Matches(snapshot.ImageDigest, snapshot.TemplateVersion) && manifest.ExternalModels == runtimeapproval.ExternalModelAdapter && (!apply || enabled) {
		return nil
	}
	if apply {
		var id, version, image string
		rows, err := tx.Query(ctx, `SELECT id::text,version::text,image_digest,runtime_manifest FROM approved_templates WHERE enabled`)
		if err != nil {
			return err
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			count++
			if err = rows.Scan(&id, &version, &image, &raw); err != nil {
				return err
			}
		}
		if rows.Err() != nil {
			return rows.Err()
		}
		manifest, err = runtimeapproval.Parse(raw)
		if count == 1 && err == nil && manifest.Matches(image, version) && manifest.ExternalModels == runtimeapproval.ExternalModelAdapter {
			snapshot.TemplateID = id
			snapshot.TemplateVersion = version
			snapshot.ImageDigest = image
			return nil
		}
	}
	return webapi.Fail(422, "CONFIGURATION_INVALID", "尚无支持外部模型的已批准运行适配器")
}
