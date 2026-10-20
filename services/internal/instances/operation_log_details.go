package instances

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

var operationLogCode = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)

func operationLogResourceType(action string) string {
	switch {
	case action == "LOGIN" || action == "LOGOUT":
		return "session"
	case strings.HasPrefix(action, "USER_") || strings.HasPrefix(action, "IDENTITY_"):
		return "user"
	case strings.HasPrefix(action, "ROLE_"):
		return "role"
	case strings.HasPrefix(action, "PERSONAL_PROVIDER_ACCOUNT_"):
		return "provider_account"
	case strings.Contains(action, "MODEL"):
		return "model"
	case strings.HasPrefix(action, "INSTANCE_") || action == "OPERATION_RETRY":
		return "instance"
	case strings.HasPrefix(action, "STORAGE_"):
		return "storage"
	case strings.HasPrefix(action, "BACKUP"):
		return "backup"
	case strings.HasPrefix(action, "RUNTIME_") || strings.HasPrefix(action, "WORKSPACE_"):
		return "runtime"
	default:
		return "resource"
	}
}

// Operation log writers may record internal maintenance reports. The portal only exposes
// explicitly typed metadata; never pass arbitrary JSON or upstream bodies through.
func safeOperationLogDetails(raw []byte) map[string]any {
	result := map[string]any{}
	if len(raw) > 1<<20 {
		return result
	}
	var values map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&values) != nil {
		return result
	}
	for _, key := range []string{"operation_id", "retry_of", "instance_id", "source_asset_id", "target_asset_id", "set_id", "template_version", "previous_template_version"} {
		if value, ok := values[key].(string); ok && idPattern.MatchString(value) {
			result[key] = value
		}
	}
	for _, key := range []string{"reason_code", "before_status", "status", "identity_source", "role_code", "before_role", "operator_ref"} {
		if value, ok := values[key].(string); ok && operationLogCode.MatchString(value) {
			result[key] = value
		}
	}
	for _, key := range []string{"schema_version", "version", "previous_version", "configuration_version", "bytes", "asset_count", "compute_capacity_units"} {
		if value, ok := operationLogNumber(values[key]); ok {
			result[key] = value
		}
	}
	for _, key := range []string{"previously_configured", "local_only"} {
		if value, ok := values[key].(bool); ok {
			result[key] = value
		}
	}
	for _, key := range []string{"permissions", "before_permissions"} {
		if values, ok := values[key].([]any); ok && len(values) <= 100 {
			permissions := []string{}
			valid := true
			for _, item := range values {
				value, ok := item.(string)
				if !ok || !operationLogCode.MatchString(value) {
					valid = false
					break
				}
				permissions = append(permissions, value)
			}
			if valid {
				result[key] = permissions
			}
		}
	}
	for _, key := range []string{"before", "after"} {
		if value, ok := values[key].(map[string]any); ok {
			limits := map[string]any{}
			for _, field := range []string{"max_instances", "cpu_millicores", "memory_bytes", "storage_bytes"} {
				if number, ok := operationLogNumber(value[field]); ok {
					limits[field] = number
				}
			}
			if len(limits) > 0 {
				result[key] = limits
			}
		}
	}
	return result
}

func operationLogNumber(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	n, err := number.Int64()
	return n, err == nil && n >= 0 && n <= 9007199254740991
}
