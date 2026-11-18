package publication

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Preference is one adjustable option in the settings panel. Future features
// add an entry to Prefs rather than a new bespoke field, so the panel stays the
// single place where users switch behaviour on and off.
type Preference struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Note    string `json:"note,omitempty"`
	Kind    string `json:"kind"`
	Section string `json:"section"`
	Default bool   `json:"default"`
	Value   bool   `json:"value"`
	// Locked means the running installation cannot change this option, e.g.
	// because the vault is read-only. Locked options are still listed so the
	// panel explains why they are unavailable.
	Locked string `json:"locked,omitempty"`
}

// preferenceDef is the static description; Value comes from the store.
type preferenceDef struct {
	Key     string
	Label   string
	Note    string
	Section string
	Default bool
}

// prefs is the registry of switchable behaviour. Order here is display order.
var prefs = []preferenceDef{
	{
		Key:     "editor.autoPreview",
		Label:   "编辑时实时预览",
		Note:    "在编辑器右侧同步渲染 Markdown。关闭后仍可手动展开预览。",
		Section: "编辑",
		Default: true,
	},
	{
		Key:     "editor.spellcheck",
		Label:   "拼写检查",
		Note:    "对 Markdown 源码启用浏览器拼写检查，适合中英混写之外的场景。",
		Section: "编辑",
		Default: false,
	},
	{
		Key:     "editor.autoSaveDraft",
		Label:   "退出前提示未保存草稿",
		Note:    "切换笔记或关闭页面前确认，避免丢失未保存内容。",
		Section: "编辑",
		Default: true,
	},
	{
		Key:     "graph.hoverPreview",
		Label:   "图谱悬停显示标题",
		Note:    "光标停在节点上时显示笔记名称与路径。",
		Section: "阅读",
		Default: true,
	},
	{
		Key:   "update.autoInstall",
		Label: "自动安装更新",
		// Default false on purpose: installing replaces the running server
		// binary, so it stays a deliberate choice rather than an opt-out.
		Note:    "检测到新版本后自动下载并安装。关闭时仅提示，需要手动点击「立即更新」。",
		Section: "更新",
		Default: false,
	},
}

// prefsFile is stored under the publication directory so a blog's options stay
// scoped to one vault, matching how publication records are isolated.
const prefsFile = "preferences.json"

type prefsState struct {
	Version int             `json:"version"`
	Values  map[string]bool `json:"values"`
}

// Preferences returns every known option with its current effective value.
// locked describes options the running installation cannot offer.
func (s *Store) Preferences(locked map[string]string) []Preference {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.preferencesLocked(locked)
}

func (s *Store) preferencesLocked(locked map[string]string) []Preference {
	result := make([]Preference, 0, len(prefs))
	for _, def := range prefs {
		value := def.Default
		if stored, ok := s.values[def.Key]; ok {
			value = stored
		}
		result = append(result, Preference{
			Key: def.Key, Label: def.Label, Note: def.Note, Kind: "toggle",
			Section: def.Section, Default: def.Default, Value: value,
			Locked: locked[def.Key],
		})
	}
	return result
}

// SetPreferences merges submitted values. Keys are ignored when unknown or
// locked, so a stale or hand-written payload cannot enable a feature the
// server does not offer.
func (s *Store) SetPreferences(values map[string]bool, locked map[string]string) ([]Preference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]bool, len(s.values)+len(values))
	for key, value := range s.values {
		next[key] = value
	}
	for key, value := range values {
		if locked[key] != "" {
			return nil, errors.New("该设置在当前模式下不可调整：" + key)
		}
		known := false
		for _, def := range prefs {
			if def.Key == key {
				known = true
				break
			}
		}
		if !known {
			return nil, errors.New("未知的设置项：" + key)
		}
		next[key] = value
	}
	if err := s.persistPrefs(next); err != nil {
		return nil, err
	}
	s.values = next
	return s.preferencesLocked(locked), nil
}

func (s *Store) persistPrefs(values map[string]bool) error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".preferences-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = json.NewEncoder(f).Encode(prefsState{Version: 1, Values: values}); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(s.dir, prefsFile))
}

// loadPreferences reads stored values. A corrupt file is an error rather than a
// silent reset, matching how publication records are validated.
func (s *Store) loadPreferences() error {
	f, err := os.Open(filepath.Join(s.dir, prefsFile))
	if errors.Is(err, os.ErrNotExist) {
		s.values = map[string]bool{}
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	var state prefsState
	decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&state); err != nil {
		return errors.New("无效的偏好设置记录")
	}
	if state.Version != 1 || state.Values == nil {
		return errors.New("无效的偏好设置记录")
	}
	known := make(map[string]bool, len(prefs))
	for _, def := range prefs {
		known[def.Key] = true
	}
	cleaned := make(map[string]bool, len(state.Values))
	for key, value := range state.Values {
		// Drop options removed from the registry instead of failing the load.
		if known[key] {
			cleaned[key] = value
		}
	}
	s.values = cleaned
	return nil
}

// PreferenceKeys is used by tests and documentation to assert the registry.
func PreferenceKeys() []string {
	keys := make([]string, 0, len(prefs))
	for _, def := range prefs {
		keys = append(keys, def.Key)
	}
	sort.Strings(keys)
	return keys
}
