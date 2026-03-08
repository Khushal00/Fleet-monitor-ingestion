package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"fleet-monitor/serving/internal/config"
)

type TimescaleStore struct {
	pool *pgxpool.Pool
}

func NewTimescaleStore(ctx context.Context, cfg *config.Config) (*TimescaleStore, error) {
	connStr := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?pool_max_conns=%d",
		cfg.DBUser,
		cfg.DBPassword,
		cfg.DBHost,
		cfg.DBPort,
		cfg.DBName,
		cfg.DBMaxConns,
	)

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to create db pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping db: %w", err)
	}

	return &TimescaleStore{pool: pool}, nil
}

func (s *TimescaleStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *TimescaleStore) Close() {
	s.pool.Close()
}
