package cache

import (
	"btc-giftcard/pkg/logger"
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Config holds Redis connection configuration parameters
type Config struct {
	Host     string
	Port     string
	Password string
	DB       int
}

// Client is the global Redis client instance used by the cache package
var Client *redis.Client

// Init initializes the Redis client with the provided configuration
// It tests the connection with a Ping command and sets the global Client variable
func Init(cfg Config) error {
	// redis options
	opts := redis.Options{
		Addr:     cfg.Host + ":" + cfg.Port,
		Password: cfg.Password, // no password set
		DB:       cfg.DB,       // use default DB
	}

	// Create Redis client
	rdb := redis.NewClient(&opts)

	// Test connection with Ping
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		logger.Error("Failed to connect to Redis", zap.Error(err))
		return err
	}

	// Set global Client variable
	Client = rdb
	logger.Info("Connected to Redis successfully", zap.String("host", cfg.Host))
	return nil
}

// Get retrieves the value of a key from Redis
// Returns empty string if key doesn't exist (redis.Nil is not treated as error)
func Get(ctx context.Context, key string) (string, error) {
	val, err := Client.Get(ctx, key).Result()
	if err == redis.Nil { // Key does not exist
		return "", nil
	} else if err != nil {
		logger.Error("Failed to get key from Redis", zap.String("key", key), zap.Error(err))
		return "", err
	}
	return val, nil
}

// Set stores a key-value pair in Redis with the specified expiration time
// Use 0 for expiration to set a key without expiration
func Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	err := Client.Set(ctx, key, value, expiration).Err()
	if err != nil {
		logger.Error("Failed to set key in Redis", zap.String("key", key), zap.Error(err))
		return err
	}
	return nil
}

// Delete removes one or more keys from Redis
// Returns the number of keys that were deleted
func Delete(ctx context.Context, keys ...string) (int64, error) {
	res, err := Client.Del(ctx, keys...).Result()
	if err != nil {
		logger.Error("Failed to delete keys from Redis", zap.Strings("keys", keys), zap.Error(err))
		return 0, err
	}
	return res, nil
}

// Exists checks if a key exists in Redis
// Returns true if the key exists, false otherwise
func Exists(ctx context.Context, key string) (bool, error) {
	res, err := Client.Exists(ctx, key).Result()
	if err != nil {
		logger.Error("Failed to check existence of key in Redis", zap.String("key", key), zap.Error(err))
		return false, err
	}
	return res > 0, nil
}

// SetNX sets a key-value pair only if the key does not exist (SET if Not eXists)
// Returns true if the key was set, false if key already exists
// Useful for distributed locking and preventing race conditions
func SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) (bool, error) {
	set, err := Client.SetNX(ctx, key, value, expiration).Result()
	if err != nil {
		logger.Error("Failed to set NX key in Redis", zap.String("key", key), zap.Error(err))
		return false, err
	}
	return set, nil
}

// Incr increments the integer value of a key by one
// If the key doesn't exist, it's set to 0 before performing the increment
// Returns the value after increment
func Incr(ctx context.Context, key string) (int64, error) {
	if Client == nil {
		return 0, errors.New("redis client not initialized")
	}
	res, err := Client.Incr(ctx, key).Result()
	if err != nil {
		logger.Error("Failed to increment key in Redis", zap.String("key", key), zap.Error(err))
		return 0, err
	}
	return res, nil
}

// Expire sets an expiration time on an existing key
// If the key already has an expiration, it will be overwritten
func Expire(ctx context.Context, key string, expiration time.Duration) error {
	err := Client.Expire(ctx, key, expiration).Err()
	if err != nil {
		logger.Error("Failed to set expiration on key in Redis", zap.String("key", key), zap.Error(err))
		return err
	}
	return nil
}

// Ping tests the Redis connection
func Ping(ctx context.Context) error {
	return Client.Ping(ctx).Err()
}

// Close closes the Redis connection
func Close() error {
	if Client != nil {
		return Client.Close()
	}
	return nil
}
