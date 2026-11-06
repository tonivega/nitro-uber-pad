// Package config handles the JSON configuration stored in ~/.nitro.uberpad.rc.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Version is overridden at build time with
// -ldflags "-X nitrobutton.com/nitro-uber-pad/internal/config.Version=...".
var Version = "v0.1"

const (
	AppName         = "Nitro UberPad"
	MaxRecent       = 100
	DefaultPageSize = 64 * 1024
	// DefaultHTTPTimeout is the default timeout (seconds) of script HTTP requests.
	DefaultHTTPTimeout = 30
)

// Action is a menu entry / toolbar button / keyboard shortcut backed by a Lua script.
type Action struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Menu     string `json:"menu"`
	Shortcut string `json:"shortcut"`
	Toolbar  bool   `json:"toolbar"`
	Script   string `json:"script"`
}

// SearchTemplate stores both the pattern and its interpretation/scope.
type SearchTemplate struct {
	Name string `json:"name"`
	Find string `json:"find"`
	Replace string `json:"replace"`
	Regex bool `json:"regex"`
	Scope string `json:"scope"`
}

type SearchUse struct {
	SearchTemplate
	Operation string `json:"operation"`
	Count int `json:"count"`
	At string `json:"at"`
}

// Config is the persisted application configuration.
type Config struct {
	SearchTemplates []SearchTemplate `json:"search_templates,omitempty"`
	SearchHistory []SearchUse `json:"search_history,omitempty"`
	PageSize int64             `json:"page_size"`
	HTTPTimeout int            `json:"http_timeout"`
	Actions  []Action          `json:"actions"`
	Vars     map[string]string `json:"vars"`
	Recent   []string          `json:"recent"`
	WindowSizes map[string]WinSize `json:"window_sizes,omitempty"`
}

// WinSize is a remembered window size.
type WinSize struct {
	W float32 `json:"w"`
	H float32 `json:"h"`
}

// Path returns the location of the configuration file.
func Path() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".nitro.uberpad.rc")
}

// ExpandHome replaces a leading ~ with the user's home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// Load reads the configuration, creating a default one when missing.
func Load() (*Config, error) {
	data, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		c := Default()
		return c, c.Save()
	}
	if err != nil {
		return Default(), err
	}
	c := &Config{}
	if err := json.Unmarshal(data, c); err != nil {
		_ = os.WriteFile(Path()+".bak", data, 0600)
		return Default(), fmt.Errorf("parsing %s (backup saved as .bak): %w", Path(), err)
	}
	c.normalize()
	return c, nil
}

func (c *Config) normalize() {
	if len(c.SearchHistory) > 100 {
		c.SearchHistory = c.SearchHistory[len(c.SearchHistory)-100:]
	}
	if c.PageSize < 4096 {
		c.PageSize = DefaultPageSize
	}
	if c.HTTPTimeout <= 0 {
		c.HTTPTimeout = DefaultHTTPTimeout
	}
	if c.Vars == nil {
		c.Vars = map[string]string{}
	}
	if len(c.Actions) == 0 {
		c.Actions = DefaultActions()
	}
	if len(c.Recent) > MaxRecent {
		c.Recent = c.Recent[:MaxRecent]
	}
}

// Save writes the configuration as JSON.
func (c *Config) Save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, Path()); err != nil {
		_ = os.Remove(Path())
		return os.Rename(tmp, Path())
	}
	return nil
}

// AddRecent pushes a file to the top of the recent list (max 100 entries).
func (c *Config) AddRecent(p string) {
	out := []string{p}
	for _, r := range c.Recent {
		if r != p {
			out = append(out, r)
		}
	}
	if len(out) > MaxRecent {
		out = out[:MaxRecent]
	}
	c.Recent = out
}

// Default returns a fresh configuration.
func Default() *Config {
	return &Config{
		PageSize:    DefaultPageSize,
		HTTPTimeout: DefaultHTTPTimeout,
		Actions:     DefaultActions(),
		Vars: map[string]string{
			"save_dir":          "~/UberPad",
			"remote_action_url": "http://127.0.0.1:8000/nup-action.lua",
			"signature": "-- \nSent from Nitro UberPad",
		},
	}
}

const smartSave = `-- Smart save: existing files are saved in place. New files ask for a
-- prefix and are stored as <prefix>-YYYYMMDD.txt inside the "save_dir" variable.
if not editor.is_new() then
  local ok, err = editor.save()
  if ok then
    ui.notify("Saved " .. editor.path())
  elseif not tostring(err):find("changed on disk", 1, true) then
    -- (when the file changed on disk a confirmation dialog is already shown)
    ui.alert("Save failed: " .. tostring(err))
  end
  return
end
ui.prompt("Save new file", "Prefix:", function(prefix)
  if prefix == nil or prefix == "" then return end
  local dir = nup.expand(config.get("save_dir") or "~/UberPad")
  nup.mkdir(dir)
  local path = nup.join(dir, prefix .. "-" .. nup.date("20060102") .. ".txt")
  local function do_save()
    local ok, err = editor.save_as(path)
    if ok then ui.notify("Saved " .. path) else ui.alert("Save failed: " .. tostring(err)) end
  end
  if nup.exists(path) then
    ui.confirm("File exists", path .. " already exists. Overwrite?", function(yes)
      if yes then do_save() end
    end)
  else
    do_save()
  end
end)
`

const remoteAction = `-- Example: a shortcut/toolbar button that runs Lua code downloaded from an
-- external HTTP server with nup.eval (like eval() in PHP). The code is fetched
-- on every run, so integrations can change without touching the editor.
-- Set the "remote_action_url" variable in Settings. Only use trusted servers!
local url = config.get("remote_action_url") or "http://127.0.0.1:8000/nup-action.lua"
local code, status, err = http.get(url)
if not code then ui.alert("Remote action: " .. tostring(err)) return end
if status ~= 200 then ui.alert("Remote action: HTTP " .. status .. " from " .. url) return end
local ok, res = nup.eval(code, "remote_action")
if not ok then ui.alert("Remote action failed: " .. tostring(res)) end
`

// DefaultActions returns the built-in scripted actions.
func DefaultActions() []Action {
	return []Action{
		{ID: "new", Label: "New", Menu: "File", Shortcut: "Mod+N", Toolbar: true, Script: "ui.new_tab()"},
		{ID: "open", Label: "Open...", Menu: "File", Shortcut: "Mod+O", Toolbar: true, Script: "ui.open_dialog()"},
		{ID: "smart_save", Label: "Save", Menu: "File", Shortcut: "Mod+S", Toolbar: true, Script: smartSave},
		{ID: "save_as", Label: "Save As...", Menu: "File", Shortcut: "Mod+Shift+S", Script: "editor.save_as_dialog()"},
		{ID: "close_tab", Label: "Close Tab", Menu: "File", Shortcut: "Mod+W", Script: "ui.close_tab()"},
		{ID: "insert_date", Label: "Insert Date/Time", Menu: "Edit", Shortcut: "Mod+D", Script: "editor.insert(nup.date(\"2006-01-02 15:04\"))"},
		{ID: "insert_signature", Label: "Insert Signature", Menu: "Edit", Shortcut: "Mod+Shift+G", Script: "editor.insert(config.get(\"signature\") or \"\")"},
		{ID: "next_page", Label: "Next Page", Menu: "View", Shortcut: "Alt+Right", Script: "local p, n = editor.page()\nif p < n then editor.goto_page(p + 1) end"},
		{ID: "prev_page", Label: "Previous Page", Menu: "View", Shortcut: "Alt+Left", Script: "local p = editor.page()\nif p > 1 then editor.goto_page(p - 1) end"},
		{ID: "remote_action", Label: "Remote Action", Menu: "Tools", Shortcut: "Mod+Shift+R", Toolbar: true, Script: remoteAction},
	}
}
