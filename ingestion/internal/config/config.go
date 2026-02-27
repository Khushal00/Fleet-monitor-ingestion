package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	// HTTP
	HTTPPort string

	// TimescaleDB
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBMaxConns int32

	// Redis
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// Pipeline channels
	DBChannelSize    int
	StateChannelSize int
	AlertChannelSize int

	// Batch writer tuning
	DBBatchSize       int
	DBFlushIntervalMS int

	// Worker counts
	DBWriterWorkers    int
	StateWriterWorkers int
	AlertWorkers       int

	// Auth
	AuthCacheTTLSeconds int
	ValidAPIKeys        []string
}

func Load() *Config {
	return &Config{
		HTTPPort:            getEnv("HTTP_PORT", "8001"),
		DBHost:              getEnv("DB_HOST", "localhost"),
		DBPort:              getEnv("DB_PORT", "5432"),
		DBUser:              getEnv("DB_USER", "fleet_user"),
		DBPassword:          getEnv("DB_PASSWORD", "fleet_password"),
		DBName:              getEnv("DB_NAME", "fleet_monitor"),
		DBMaxConns:          int32(getEnvInt("DB_MAX_CONNS", 15)),
		RedisAddr:           getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:       getEnv("REDIS_PASSWORD", ""),
		RedisDB:             getEnvInt("REDIS_DB", 0),
		DBChannelSize:       getEnvInt("DB_CHANNEL_SIZE", 10000),
		StateChannelSize:    getEnvInt("STATE_CHANNEL_SIZE", 50000),
		AlertChannelSize:    getEnvInt("ALERT_CHANNEL_SIZE", 10000),
		DBBatchSize:         getEnvInt("DB_BATCH_SIZE", 500),
		DBFlushIntervalMS:   getEnvInt("DB_FLUSH_INTERVAL_MS", 100),
		DBWriterWorkers:     getEnvInt("DB_WRITER_WORKERS", 10),
		StateWriterWorkers:  getEnvInt("STATE_WRITER_WORKERS", 5),
		AlertWorkers:        getEnvInt("ALERT_WORKERS", 3),
		AuthCacheTTLSeconds: getEnvInt("AUTH_CACHE_TTL_SECONDS", 300),
		ValidAPIKeys:        strings.Split(getEnv("VALID_API_KEYS", ""), ","),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
