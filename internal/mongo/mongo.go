package mongo

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	defaultURI             = "mongodb://localhost:27017"
	defaultDBName          = "go_task_queue"
	defaultDLQCollection   = "dlq_jobs"
	defaultLogCollection   = "log_entries"
	envMongoURI            = "MONGODB_URI"
	envMongoDB             = "MONGO_DB"
	envMongoDLQCollection  = "MONGO_DLQ_COLLECTION"
	envMongoLogCollection  = "MONGO_LOG_COLLECTION"
	defaultConnectTimeout  = 5 * time.Second
	defaultOperationTimeout = 5 * time.Second
)

// Client wraps a mongo.Client plus lazily-resolved DB and collections.
type Client struct {
	client *mongo.Client
	db     *mongo.Database

	dlqColl *mongo.Collection
	logColl *mongo.Collection
}

var (
	once     sync.Once
	instance *Client
	initErr  error
)

// NewClient initializes a singleton Mongo client using environment variables.
// It is safe to call multiple times; the same instance is returned.
func NewClient(ctx context.Context) (*Client, error) {
	once.Do(func() {
		uri := getenvDefault(envMongoURI, defaultURI)
		dbName := getenvDefault(envMongoDB, defaultDBName)

		opt := options.Client().ApplyURI(uri)

		connectCtx, cancel := context.WithTimeout(ctx, defaultConnectTimeout)
		defer cancel()

		mc, err := mongo.Connect(connectCtx, opt)
		if err != nil {
			initErr = fmt.Errorf("mongo connect: %w", err)
			return
		}

		pingCtx, pingCancel := context.WithTimeout(ctx, defaultOperationTimeout)
		defer pingCancel()
		if err := mc.Ping(pingCtx, nil); err != nil {
			initErr = fmt.Errorf("mongo ping: %w", err)
			_ = mc.Disconnect(context.Background())
			return
		}

		db := mc.Database(dbName)
		instance = &Client{
			client: mc,
			db:     db,
			dlqColl: db.Collection(getenvDefault(envMongoDLQCollection, defaultDLQCollection)),
			logColl: db.Collection(getenvDefault(envMongoLogCollection, defaultLogCollection)),
		}
	})
	return instance, initErr
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// DLQCollection returns the Mongo collection used for DLQ jobs.
func (c *Client) DLQCollection() *mongo.Collection {
	return c.dlqColl
}

// LogCollection returns the Mongo collection used for log entries.
func (c *Client) LogCollection() *mongo.Collection {
	return c.logColl
}

// Disconnect closes the underlying Mongo client.
func (c *Client) Disconnect(ctx context.Context) error {
	return c.client.Disconnect(ctx)
}

