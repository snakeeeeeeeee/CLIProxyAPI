package resourcepool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ReserveHealthyProxies atomically assigns healthy, unbound proxies to item IDs.
func (s *Store) ReserveHealthyProxies(ctx context.Context, ownerID, purpose string, itemIDs []string, ttl time.Duration) ([]ProxyReservation, error) {
	return s.reserveAvailableProxies(ctx, ownerID, purpose, itemIDs, ttl, true)
}

// ReserveAvailableProxies atomically assigns healthy-or-unknown, unbound proxies.
func (s *Store) ReserveAvailableProxies(ctx context.Context, ownerID, purpose string, itemIDs []string, ttl time.Duration) ([]ProxyReservation, error) {
	return s.reserveAvailableProxies(ctx, ownerID, purpose, itemIDs, ttl, false)
}

func (s *Store) reserveAvailableProxies(ctx context.Context, ownerID, purpose string, itemIDs []string, ttl time.Duration, healthyOnly bool) ([]ProxyReservation, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	ownerID = strings.TrimSpace(ownerID)
	purpose = strings.TrimSpace(purpose)
	if ownerID == "" || purpose == "" {
		return nil, fmt.Errorf("reservation owner and purpose are required")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("reservation TTL must be positive")
	}
	items := normalizeReservationItems(itemIDs)
	if len(items) == 0 {
		return []ProxyReservation{}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin proxy reservation: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `DELETE FROM proxy_reservations WHERE expires_at <= ?`, dbTime(now)); err != nil {
		return nil, fmt.Errorf("remove expired proxy reservations: %w", err)
	}
	healthClause := "AND (p.health_status = 'healthy' OR p.health_status = 'unknown')"
	if healthyOnly {
		healthClause = "AND p.health_status = 'healthy'"
	}
	rows, err := tx.QueryContext(ctx, `
SELECT p.id
FROM proxy_resources p
LEFT JOIN claude_code_accounts a ON a.proxy_resource_id = p.id
LEFT JOIN proxy_reservations r ON r.proxy_resource_id = p.id AND r.expires_at > ?
WHERE p.enabled = 1 `+healthClause+` AND a.id IS NULL AND r.proxy_resource_id IS NULL
ORDER BY p.updated_at ASC, p.id ASC
LIMIT ?
`, dbTime(now), len(items))
	if err != nil {
		return nil, fmt.Errorf("select proxies for reservation: %w", err)
	}
	proxyIDs := make([]string, 0, len(items))
	for rows.Next() {
		var proxyID string
		if err := rows.Scan(&proxyID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan reserved proxy: %w", err)
		}
		proxyIDs = append(proxyIDs, proxyID)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close reserved proxy rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reserved proxies: %w", err)
	}
	expiresAt := now.Add(ttl)
	reservations := make([]ProxyReservation, 0, len(proxyIDs))
	for index, proxyID := range proxyIDs {
		itemID := items[index]
		if _, err := tx.ExecContext(ctx, `
INSERT INTO proxy_reservations(proxy_resource_id, owner_id, item_id, purpose, expires_at, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?)
`, proxyID, ownerID, itemID, purpose, dbTime(expiresAt), dbTime(now), dbTime(now)); err != nil {
			return nil, mapSQLiteConstraintError(err, "proxy reservation")
		}
		reservations = append(reservations, ProxyReservation{
			ProxyResourceID: proxyID,
			OwnerID:         ownerID,
			ItemID:          itemID,
			Purpose:         purpose,
			ExpiresAt:       expiresAt,
			CreatedAt:       now,
			UpdatedAt:       now,
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit proxy reservations: %w", err)
	}
	return reservations, nil
}

// ReserveProxy holds one explicitly selected proxy when it is available.
func (s *Store) ReserveProxy(ctx context.Context, proxyID, ownerID, itemID, purpose string, ttl time.Duration) (*ProxyReservation, error) {
	proxyID = strings.TrimSpace(proxyID)
	ownerID = strings.TrimSpace(ownerID)
	itemID = strings.TrimSpace(itemID)
	purpose = strings.TrimSpace(purpose)
	if proxyID == "" || ownerID == "" || itemID == "" || purpose == "" || ttl <= 0 {
		return nil, fmt.Errorf("proxy, reservation identity, purpose, and positive TTL are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin explicit proxy reservation: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `DELETE FROM proxy_reservations WHERE expires_at <= ?`, dbTime(now)); err != nil {
		return nil, fmt.Errorf("remove expired proxy reservations: %w", err)
	}
	if err := assertProxyBindableTx(ctx, tx, "", proxyID, "", ""); err != nil {
		return nil, err
	}
	expiresAt := now.Add(ttl)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO proxy_reservations(proxy_resource_id, owner_id, item_id, purpose, expires_at, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?)
`, proxyID, ownerID, itemID, purpose, dbTime(expiresAt), dbTime(now), dbTime(now)); err != nil {
		return nil, mapSQLiteConstraintError(err, "proxy reservation")
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit explicit proxy reservation: %w", err)
	}
	return &ProxyReservation{
		ProxyResourceID: proxyID, OwnerID: ownerID, ItemID: itemID, Purpose: purpose,
		ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// RenewProxyReservations extends all active reservations owned by one job.
func (s *Store) RenewProxyReservations(ctx context.Context, ownerID string, ttl time.Duration) error {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || ttl <= 0 {
		return fmt.Errorf("reservation owner and positive TTL are required")
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
UPDATE proxy_reservations
SET expires_at = ?, updated_at = ?
WHERE owner_id = ? AND expires_at > ?
`, dbTime(now.Add(ttl)), dbTime(now), ownerID, dbTime(now))
	if err != nil {
		return fmt.Errorf("renew proxy reservations: %w", err)
	}
	return nil
}

// ReleaseProxyReservation releases one job item reservation.
func (s *Store) ReleaseProxyReservation(ctx context.Context, ownerID, itemID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM proxy_reservations WHERE owner_id = ? AND item_id = ?`, strings.TrimSpace(ownerID), strings.TrimSpace(itemID))
	if err != nil {
		return fmt.Errorf("release proxy reservation: %w", err)
	}
	return nil
}

// ReleaseProxyReservations releases every reservation owned by one job.
func (s *Store) ReleaseProxyReservations(ctx context.Context, ownerID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM proxy_reservations WHERE owner_id = ?`, strings.TrimSpace(ownerID))
	if err != nil {
		return fmt.Errorf("release proxy reservations: %w", err)
	}
	return nil
}

// GetProxyReservation returns one active reservation for an owner item.
func (s *Store) GetProxyReservation(ctx context.Context, ownerID, itemID string) (*ProxyReservation, error) {
	var reservation ProxyReservation
	var expiresRaw, createdRaw, updatedRaw string
	err := s.db.QueryRowContext(ctx, `
SELECT proxy_resource_id, owner_id, item_id, purpose, expires_at, created_at, updated_at
FROM proxy_reservations
WHERE owner_id = ? AND item_id = ? AND expires_at > ?
`, strings.TrimSpace(ownerID), strings.TrimSpace(itemID), dbTime(time.Now())).Scan(
		&reservation.ProxyResourceID,
		&reservation.OwnerID,
		&reservation.ItemID,
		&reservation.Purpose,
		&expiresRaw,
		&createdRaw,
		&updatedRaw,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("get proxy reservation: %w", err)
	}
	reservation.ExpiresAt = parseDBTime(expiresRaw)
	reservation.CreatedAt = parseDBTime(createdRaw)
	reservation.UpdatedAt = parseDBTime(updatedRaw)
	return &reservation, nil
}

func normalizeReservationItems(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
