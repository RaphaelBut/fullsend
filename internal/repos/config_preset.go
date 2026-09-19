package repos

import (
	"context"
	"fmt"
	"sync"

	"github.com/fullsend-ai/fullsend/internal/config"
	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/preset"
)

// presetCache fetches and validates configuration presets once per
// unique (source, hash) pair so a fleet of repos sharing defaults.config
// does not re-download the same document.
type presetCache struct {
	mu    sync.Mutex
	items map[string]presetCacheEntry
}

type presetCacheEntry struct {
	data []byte
	err  error
}

func newPresetCache() *presetCache {
	return &presetCache{items: make(map[string]presetCacheEntry)}
}

func (c *presetCache) Load(ctx context.Context, source, hash string) ([]byte, error) {
	if source == "" {
		return nil, nil
	}
	key := source + "\x00" + hash
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		return e.data, e.err
	}
	data, err := loadConfigPreset(ctx, source, hash)
	c.items[key] = presetCacheEntry{data: data, err: err}
	return data, err
}

// loadConfigPreset fetches a preset via the shared preset package, then
// checks that it is a syntactically and structurally valid per-repo
// config layer. Hash verification uses the same semantics as
// `github setup --config-hash`.
func loadConfigPreset(ctx context.Context, source, hash string) ([]byte, error) {
	data, err := preset.Load(ctx, source, hash)
	if err != nil {
		return nil, err
	}
	if err := validatePresetAsPerRepo(data); err != nil {
		return nil, err
	}
	return data, nil
}

func validatePresetAsPerRepo(data []byte) error {
	if !config.IsPerRepoYAML(data) {
		return fmt.Errorf("preset is not a per-repo configuration")
	}
	w, err := config.ParsePerRepoConfigWriter(data)
	if err != nil {
		return fmt.Errorf("parsing preset: %w", err)
	}
	if err := w.Validate(); err != nil {
		return fmt.Errorf("invalid preset: %w", err)
	}
	return nil
}

// convergePresetFiles returns the base-layer file to write when a
// declared preset differs from the installed .fullsend/config.base.yaml.
// The overlay is never returned. An empty preset (no source declared)
// is a no-op so an existing base file is preserved without comparison.
func convergePresetFiles(ctx context.Context, resolved ResolvedConfig, presetBytes []byte, dryRun bool, progress ProgressFunc) ([]forge.TreeFile, []ComponentAction) {
	if len(presetBytes) == 0 {
		return nil, nil
	}

	repoFullName := resolved.Owner + "/" + resolved.Repo
	client := resolved.ForgeConfig.Client
	var actions []ComponentAction

	existing, err := client.GetFileContent(ctx, resolved.Owner, resolved.Repo, preset.BasePath)
	if err != nil {
		if !forge.IsNotFound(err) {
			actions = append(actions, ComponentAction{
				Component: preset.BasePath,
				Action:    "error",
				Detail:    fmt.Sprintf("reading %s: %v", preset.BasePath, err),
			})
			return nil, actions
		}
		existing = nil
	}

	plan := preset.Apply(presetBytes, existing, nil)
	if !plan.BaseChanged {
		return nil, nil
	}

	action := "update"
	detail := fmt.Sprintf("updated %s from declared preset", preset.BasePath)
	if len(existing) == 0 {
		action = "add"
		detail = fmt.Sprintf("added %s from declared preset", preset.BasePath)
	}
	if dryRun {
		if action == "add" {
			detail = fmt.Sprintf("would add %s from declared preset", preset.BasePath)
		} else {
			detail = fmt.Sprintf("would update %s from declared preset", preset.BasePath)
		}
		actions = append(actions, ComponentAction{
			Component: preset.BasePath,
			Action:    action,
			Detail:    detail,
		})
		progress(repoFullName, "dry-run", detail)
		return nil, actions
	}

	progress(repoFullName, "preset", detail)
	actions = append(actions, ComponentAction{
		Component: preset.BasePath,
		Action:    action,
		Detail:    detail,
	})
	return []forge.TreeFile{{
		Path:    preset.BasePath,
		Content: plan.Base,
		Mode:    "100644",
	}}, actions
}

// checkPresetDrift reports base-file drift when a preset is declared.
// When no preset is declared the existing base is left uncompared.
func checkPresetDrift(ctx context.Context, cfg ResolvedConfig, store *presetCache, status *RepoStatus) {
	if cfg.Config == "" {
		return
	}
	data, err := store.Load(ctx, cfg.Config, cfg.ConfigHash)
	if err != nil {
		status.Error = fmt.Sprintf("loading config preset for %s/%s: %v", cfg.Owner, cfg.Repo, err)
		return
	}
	existing, readErr := cfg.ForgeConfig.Client.GetFileContent(ctx, cfg.Owner, cfg.Repo, preset.BasePath)
	if readErr != nil && !forge.IsNotFound(readErr) {
		status.Error = fmt.Sprintf("reading %s for %s/%s: %v", preset.BasePath, cfg.Owner, cfg.Repo, readErr)
		return
	}
	if forge.IsNotFound(readErr) {
		existing = nil
	}
	plan := preset.Apply(data, existing, nil)
	if !plan.BaseChanged {
		return
	}
	actual := "installed content differs"
	if len(existing) == 0 {
		actual = "missing"
	}
	status.Drifts = append(status.Drifts, Drift{
		Field:    preset.BasePath,
		Expected: "declared preset",
		Actual:   actual,
	})
}
