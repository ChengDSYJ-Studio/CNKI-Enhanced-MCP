// Removes only this service's client configuration entry.

package cnki

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// UninstallClient removes only this server's configuration entry. Runtime,
// browser profile, research records and downloads are deliberately retained.
func UninstallClient(client string) (any, error) {
	spec, ok := clientSpec(client)
	if !ok {
		return nil, fail("UNKNOWN_CLIENT", "未知客户端")
	}
	path := clientPath(client)
	if path == "" {
		return nil, fail("CLIENT_IMPORT_REQUIRED", "该客户端需要在其设置界面删除知网服务")
	}
	removed, err := removeClientEntry(spec, path)
	return map[string]any{"client": client, "config_path": path, "removed": removed, "data_retained": true}, err
}

func removeClientEntry(spec ClientSpec, path string) (bool, error) {
	before, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info, e := os.Lstat(path); e != nil || info.Mode()&os.ModeSymlink != 0 {
		return false, fail("CONFIG_SYMLINK", "拒绝修改符号链接配置")
	}
	var after []byte
	removed := false
	switch spec.Format {
	case "toml":
		var current map[string]any
		if err = toml.Unmarshal(before, &current); err != nil {
			return false, fail("CONFIG_INVALID", "原 TOML 无法解析")
		}
		servers, ok := current["mcp_servers"].(map[string]any)
		if !ok {
			return false, nil
		}
		old, exists := servers["cnki"]
		if !exists {
			return false, nil
		}
		if !ownedConfigEntry(old) {
			return false, fail("CONFIG_NAME_IN_USE", "现有 cnki 配置不属于本服务")
		}
		delete(servers, "cnki")
		removed = true
		after, err = toml.Marshal(current)
	case "dsh":
		var current []map[string]any
		if err = yaml.Unmarshal(before, &current); err != nil {
			return false, fail("CONFIG_INVALID", "原 YAML 无法解析")
		}
		keptEntries := []map[string]any{}
		for _, item := range current {
			modules, ok := item["insert"].([]any)
			if !ok {
				keptEntries = append(keptEntries, item)
				continue
			}
			keep := []any{}
			changed := false
			for _, module := range modules {
				m, ok := module.(map[string]any)
				if ok && m["id"] == "mcp-cnki" {
					removed = true
					changed = true
					continue
				}
				keep = append(keep, module)
			}
			if changed && len(keep) == 0 && len(item) == 1 {
				continue
			}
			item["insert"] = keep
			keptEntries = append(keptEntries, item)
		}
		if !removed {
			return false, nil
		}
		after, err = yaml.Marshal(keptEntries)
	default:
		var current map[string]any
		if err = json.Unmarshal(before, &current); err != nil {
			return false, fail("CONFIG_INVALID", "原 JSON 无法解析")
		}
		node := current
		for _, part := range strings.Split(spec.key(), ".") {
			next, ok := node[part].(map[string]any)
			if !ok {
				return false, nil
			}
			node = next
		}
		old, exists := node["cnki"]
		if !exists {
			return false, nil
		}
		if !ownedConfigEntry(old) {
			return false, fail("CONFIG_NAME_IN_USE", "现有 cnki 配置不属于本服务")
		}
		delete(node, "cnki")
		removed = true
		after, err = json.MarshalIndent(current, "", "  ")
		after = append(after, '\n')
	}
	if err != nil {
		return false, err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".cnki-config-")
	if err != nil {
		return false, err
	}
	defer os.Remove(temp.Name())
	if err = temp.Chmod(0600); err == nil {
		_, err = temp.Write(after)
	}
	if err == nil {
		err = temp.Sync()
	}
	ce := temp.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return false, err
	}
	latest, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if string(latest) != string(before) {
		return false, fail("CONFIG_CHANGED", "配置已被其他程序修改，未覆盖")
	}
	return removed, os.Rename(temp.Name(), path)
}
