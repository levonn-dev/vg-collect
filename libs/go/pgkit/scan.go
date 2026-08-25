package pgkit

import "github.com/jackc/pgx/v5"

// ScanAll drains rows into a slice, closing them once done. seed sets the zero-row result
// (nil, or []T{}), letting each caller keep its own zero-row contract.
func ScanAll[T any](rows pgx.Rows, seed []T, scan func(pgx.Rows) (T, error)) ([]T, error) {
	defer rows.Close()
	out := seed
	for rows.Next() {
		x, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
