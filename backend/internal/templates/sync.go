package templates

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"wacampaigns/internal/bird"
)

// Sync pulls the workspace's templates from Bird, with the content of each
// template's default language, and upserts them. Templates deleted in Bird
// are removed locally.
func Sync(ctx context.Context, db *pgxpool.Pool, bc *bird.Client) (int, error) {
	raws, err := bc.ListTemplates(ctx)
	if err != nil {
		return 0, err
	}
	slugs := make([]string, 0, len(raws))
	for _, raw := range raws {
		var t bird.Template
		if err := json.Unmarshal(raw, &t); err != nil || t.Slug == "" {
			continue
		}
		slugs = append(slugs, t.Slug)

		// Content lives under a version: prefer the live one, else the pending
		// or draft one so new submissions can still be previewed.
		version := ""
		for _, v := range []*string{t.LiveVersionID, t.PendingVersionID, t.DraftVersionID} {
			if v != nil && *v != "" {
				version = *v
				break
			}
		}
		components := json.RawMessage("[]")
		if version != "" && t.DefaultLanguage != "" {
			if lang, err := bc.GetTemplateLanguage(ctx, t.Slug, version, t.DefaultLanguage); err == nil && len(lang.Components) > 0 {
				components = lang.Components
			} else if err != nil {
				slog.Warn("template content fetch failed", "slug", t.Slug, "err", err)
			}
		}
		langs, _ := json.Marshal(t.Languages)
		if t.AvailableLanguages == nil {
			t.AvailableLanguages = []string{}
		}
		live := ""
		if t.LiveVersionID != nil {
			live = *t.LiveVersionID
		}
		_, err := db.Exec(ctx, `
			INSERT INTO templates (bird_id, slug, name, category, status, scope, default_language,
				languages, available_languages, live_version_id, components, raw, synced_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now())
			ON CONFLICT (slug) DO UPDATE SET bird_id=EXCLUDED.bird_id, name=EXCLUDED.name,
				category=EXCLUDED.category, status=EXCLUDED.status, scope=EXCLUDED.scope,
				default_language=EXCLUDED.default_language, languages=EXCLUDED.languages,
				available_languages=EXCLUDED.available_languages, live_version_id=EXCLUDED.live_version_id,
				components=CASE WHEN EXCLUDED.components = '[]'::jsonb THEN templates.components ELSE EXCLUDED.components END,
				raw=EXCLUDED.raw, synced_at=now()`,
			t.ID, t.Slug, t.Name, t.Category, t.Status, t.Scope, t.DefaultLanguage,
			langs, t.AvailableLanguages, live, components, raw)
		if err != nil {
			return 0, err
		}
	}
	if _, err := db.Exec(ctx, `DELETE FROM templates WHERE NOT (slug = ANY($1))`, slugs); err != nil {
		return 0, err
	}
	return len(slugs), nil
}
