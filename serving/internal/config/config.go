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

	// Auth
	ValidAPIKeys        []string
	AuthCacheTTLSeconds int

	// Background jobs
	HeartbeatIntervalSeconds         int
	StalenessThresholdSeconds        int
	StopDetectorIntervalSeconds      int
	DeviationDetectorIntervalSeconds int
}

func Load() *Config {
	return &Config{
		HTTPPort:   getEnv("HTTP_PORT", "8002"),
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "fleet_user"),
		DBPassword: getEnv("DB_PASSWORD", "fleet_password"),
		DBName:     getEnv("DB_NAME", "fleet_monitor"),
		DBMaxConns: int32(getEnvInt("DB_MAX_CONNS", 10)),

		RedisAddr:     getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getEnvInt("REDIS_DB", 0),

		ValidAPIKeys:        strings.Split(getEnv("VALID_API_KEYS", ""), ","),
		AuthCacheTTLSeconds: getEnvInt("AUTH_CACHE_TTL_SECONDS", 300),

		HeartbeatIntervalSeconds:         getEnvInt("HEARTBEAT_INTERVAL_SECONDS", 30),
		StalenessThresholdSeconds:        getEnvInt("STALENESS_THRESHOLD_SECONDS", 60),
		StopDetectorIntervalSeconds:      getEnvInt("STOP_DETECTOR_INTERVAL_SECONDS", 30),
		DeviationDetectorIntervalSeconds: getEnvInt("DEVIATION_DETECTOR_INTERVAL_SECONDS", 60),
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
