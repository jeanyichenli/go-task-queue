package dlq

import (
	"context"
	"time"

	"go-task-queue/internal/job"
	mongoclient "go-task-queue/internal/mongo"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// DLQ represents the dead letter queue backed by MongoDB.
type DLQ interface {
	MoveToDLQ(ctx context.Context, j *job.Job, reason string) error
	GetDLQJob(ctx context.Context, jobID string) (*DLQJob, error)
	ListDLQJobs(ctx context.Context, filter Filter) ([]*DLQJob, error)
	RequeueDLQJob(ctx context.Context, jobID string) (*DLQJob, error)
	DeleteDLQJob(ctx context.Context, jobID string) error
	Metrics(ctx context.Context) (*Metrics, error)
}

// DLQJob is the MongoDB representation of a job in the dead letter queue.
type DLQJob struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	JobID       string             `bson:"job_id" json:"job_id"`
	Type        string             `bson:"type" json:"type"`
	Payload     map[string]any     `bson:"payload" json:"payload"`
	Status      job.Status         `bson:"status" json:"status"`
	Attempt     int                `bson:"attempt" json:"attempt"`
	MaxAttempts int                `bson:"max_attempts" json:"max_attempts"`
	LastError   string             `bson:"last_error" json:"last_error"`

	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
	FailedAt  time.Time `bson:"failed_at" json:"failed_at"`

	Reason     string     `bson:"reason" json:"reason"`
	RequeuedAt *time.Time `bson:"requeued_at,omitempty" json:"requeued_at,omitempty"`
}

// Filter controls listing of DLQ jobs.
type Filter struct {
	Type  string
	Limit int64
	Skip  int64
}

// Metrics summarizes DLQ statistics.
type Metrics struct {
	Total          int64            `json:"total"`
	ByType         map[string]int64 `json:"by_type"`
	OldestFailedAt *time.Time       `json:"oldest_failed_at,omitempty"`
	NewestFailedAt *time.Time       `json:"newest_failed_at,omitempty"`
}

// MongoDLQ implements DLQ using MongoDB collections.
type MongoDLQ struct {
	coll *mongo.Collection
}

// NewMongoDLQ constructs a MongoDLQ using the shared mongo client.
func NewMongoDLQ(ctx context.Context) (*MongoDLQ, error) {
	mc, err := mongoclient.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return &MongoDLQ{coll: mc.DLQCollection()}, nil
}

// NewMongoDLQWithCollection is a helper primarily intended for tests, allowing
// callers to provide a specific Mongo collection.
func NewMongoDLQWithCollection(coll *mongo.Collection) *MongoDLQ {
	return &MongoDLQ{coll: coll}
}

func (d *MongoDLQ) MoveToDLQ(ctx context.Context, j *job.Job, reason string) error {
	now := time.Now()
	doc := &DLQJob{
		JobID:       j.ID,
		Type:        j.Type,
		Payload:     j.Payload,
		Status:      job.StatusDeadLetter,
		Attempt:     j.Attempt,
		MaxAttempts: j.MaxAttempts,
		LastError:   j.LastError,
		CreatedAt:   j.CreatedAt,
		UpdatedAt:   j.UpdatedAt,
		FailedAt:    now,
		Reason:      reason,
	}
	_, err := d.coll.InsertOne(ctx, doc)
	return err
}

func (d *MongoDLQ) GetDLQJob(ctx context.Context, jobID string) (*DLQJob, error) {
	var out DLQJob
	err := d.coll.FindOne(ctx, bson.M{"job_id": jobID}).Decode(&out)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (d *MongoDLQ) ListDLQJobs(ctx context.Context, filter Filter) ([]*DLQJob, error) {
	q := bson.M{}
	if filter.Type != "" {
		q["type"] = filter.Type
	}

	opts := options.Find()
	if filter.Limit > 0 {
		opts.SetLimit(filter.Limit)
	}
	if filter.Skip > 0 {
		opts.SetSkip(filter.Skip)
	}
	opts.SetSort(bson.D{{Key: "failed_at", Value: -1}})

	cur, err := d.coll.Find(ctx, q, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var out []*DLQJob
	for cur.Next(ctx) {
		var dj DLQJob
		if err := cur.Decode(&dj); err != nil {
			return nil, err
		}
		out = append(out, &dj)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *MongoDLQ) RequeueDLQJob(ctx context.Context, jobID string) (*DLQJob, error) {
	var dj DLQJob
	err := d.coll.FindOne(ctx, bson.M{"job_id": jobID}).Decode(&dj)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	now := time.Now()
	_, err = d.coll.UpdateOne(ctx, bson.M{"_id": dj.ID}, bson.M{
		"$set": bson.M{
			"requeued_at": now,
		},
	})
	if err != nil {
		return nil, err
	}
	return &dj, nil
}

func (d *MongoDLQ) DeleteDLQJob(ctx context.Context, jobID string) error {
	_, err := d.coll.DeleteOne(ctx, bson.M{"job_id": jobID})
	if err == mongo.ErrNoDocuments {
		return nil
	}
	return err
}

func (d *MongoDLQ) Metrics(ctx context.Context) (*Metrics, error) {
	total, err := d.coll.CountDocuments(ctx, bson.M{})
	if err != nil {
		return nil, err
	}

	byType := make(map[string]int64)
	pipeline := mongo.Pipeline{
		{{Key: "$group", Value: bson.M{"_id": "$type", "count": bson.M{"$sum": 1}}}},
	}
	cur, err := d.coll.Aggregate(ctx, pipeline)
	if err == nil {
		defer cur.Close(ctx)
		for cur.Next(ctx) {
			var row struct {
				ID    string `bson:"_id"`
				Count int64  `bson:"count"`
			}
			if err := cur.Decode(&row); err != nil {
				return nil, err
			}
			byType[row.ID] = row.Count
		}
		if err := cur.Err(); err != nil {
			return nil, err
		}
	}

	var oldest, newest *time.Time
	optsAsc := options.FindOne().SetSort(bson.D{{Key: "failed_at", Value: 1}})
	var tmp DLQJob
	if err := d.coll.FindOne(ctx, bson.M{}, optsAsc).Decode(&tmp); err == nil {
		t := tmp.FailedAt
		oldest = &t
	}
	optsDesc := options.FindOne().SetSort(bson.D{{Key: "failed_at", Value: -1}})
	if err := d.coll.FindOne(ctx, bson.M{}, optsDesc).Decode(&tmp); err == nil {
		t := tmp.FailedAt
		newest = &t
	}

	return &Metrics{
		Total:          total,
		ByType:         byType,
		OldestFailedAt: oldest,
		NewestFailedAt: newest,
	}, nil
}

