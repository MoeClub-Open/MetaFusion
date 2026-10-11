package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
)

// PostgreSQL supplies both the clock and the atomic counter. A hashed subject
// avoids retaining an anonymous IP address in this operational table.
func consumeRateWindow(ctx context.Context, db *sql.DB, subject string) (used, retrySeconds int, err error) {
	sum := sha256.Sum256([]byte(subject))
	err = db.QueryRowContext(ctx, `
		INSERT INTO catalog.api_rate_windows(subject_key, window_start, used)
		VALUES ($1, date_trunc('minute', clock_timestamp()), 1)
		ON CONFLICT (subject_key) DO UPDATE SET
			used = CASE WHEN api_rate_windows.window_start < EXCLUDED.window_start THEN 1
				ELSE LEAST(api_rate_windows.used::bigint + 1, 2147483647)::integer END,
			window_start = GREATEST(api_rate_windows.window_start, EXCLUDED.window_start)
		RETURNING used, GREATEST(1, CEIL(EXTRACT(EPOCH FROM
			(window_start + interval '1 minute' - clock_timestamp()))))::integer`,
		hex.EncodeToString(sum[:])).Scan(&used, &retrySeconds)
	return
}

func (s *Store) pruneRateWindows(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM catalog.api_rate_windows
		WHERE window_start < now() - interval '2 hours' AND subject_key IN (
		SELECT subject_key FROM catalog.api_rate_windows
		WHERE window_start < now() - interval '2 hours'
		ORDER BY window_start LIMIT 5000)`)
	return err
}
