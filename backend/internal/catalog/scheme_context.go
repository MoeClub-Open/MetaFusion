package catalog

import (
	"context"
	"fmt"
)

// 改动载体格式时回放现有曲目，避免父节点一改就让子节点的定位方案失效。
func validateMediumSchemeChange(ctx context.Context, q queryer, d Definitions, old, next Entity) error {
	oldFormat, _ := old.Attributes["format"].(string)
	format, _ := next.Attributes["format"].(string)
	if old.ID == "" || next.Kind != "medium" || oldFormat == format {
		return nil
	}
	hasFormatScheme := false
	for _, scheme := range d.Schemes {
		if scheme.Enabled && scheme.MediumFormats != nil && len(*scheme.MediumFormats) > 0 {
			hasFormatScheme = true
			break
		}
	}
	if !hasFormatScheme {
		return nil
	}
	rows, err := q.QueryContext(ctx, "SELECT id FROM catalog.tracks WHERE medium_id=$1", next.ID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	allowReference := func(string, []string) error { return nil }
	for _, id := range ids {
		track, err := get(ctx, q, id)
		if err != nil {
			return err
		}
		if track.State == "deleted" || track.State == "merged" {
			continue
		}
		if err = d.validateEntity(track, allowReference, true, format); err != nil {
			return fmt.Errorf("track_scheme_conflict: %s: %w", id, err)
		}
	}
	return nil
}
