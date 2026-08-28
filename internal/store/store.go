package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type MessageKind string

const (
	KindDigest   MessageKind = "DIGEST"
	KindReminder MessageKind = "REMINDER"
)

type SnapshotRow struct {
	Entry int    `dynamodbav:"entry"`
	Name  string `dynamodbav:"name"`
	Rank  int    `dynamodbav:"rank"`
	Total int    `dynamodbav:"total"`
}

type Snapshot struct {
	GW      int           `dynamodbav:"gw"`
	TakenAt string        `dynamodbav:"takenAt"`
	Rows    []SnapshotRow `dynamodbav:"rows"`
}

// API is the slice of DynamoDB this package uses, so tests can fake it.
type API interface {
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

type Store struct {
	db       API
	table    string
	leagueID int
	now      func() time.Time
}

func New(db API, table string, leagueID int) *Store {
	return &Store{db: db, table: table, leagueID: leagueID, now: time.Now}
}

func (s *Store) pk() string { return fmt.Sprintf("LEAGUE#%d", s.leagueID) }

// Gameweeks are zero-padded so range queries sort correctly (GW9 before GW10).
func gwKey(gw int) string { return fmt.Sprintf("%02d", gw) }

func (s *Store) sentSK(kind MessageKind, gw int) string {
	return fmt.Sprintf("SENT#GW#%s#%s", gwKey(gw), kind)
}

func key(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: pk},
		"sk": &types.AttributeValueMemberS{Value: sk},
	}
}

// Claim reserves the right to send one message, returning false if it is
// already taken. This is what makes repeat ticks and Lambda retries safe.
func (s *Store) Claim(ctx context.Context, kind MessageKind, gw int) (bool, error) {
	item := key(s.pk(), s.sentSK(kind, gw))
	item["gw"] = &types.AttributeValueMemberN{Value: fmt.Sprint(gw)}
	item["kind"] = &types.AttributeValueMemberS{Value: string(kind)}
	item["claimedAt"] = &types.AttributeValueMemberS{Value: s.now().UTC().Format(time.RFC3339)}

	_, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.table),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(pk)"),
	})
	if err != nil {
		var failed *types.ConditionalCheckFailedException
		if errors.As(err, &failed) {
			return false, nil
		}
		return false, fmt.Errorf("claim %s gw%d: %w", kind, gw, err)
	}
	return true, nil
}

// Release undoes a claim when the send fails, so the next tick retries rather
// than the message being lost.
func (s *Store) Release(ctx context.Context, kind MessageKind, gw int) error {
	_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.table),
		Key:       key(s.pk(), s.sentSK(kind, gw)),
	})
	if err != nil {
		return fmt.Errorf("release %s gw%d: %w", kind, gw, err)
	}
	return nil
}

func (s *Store) PutSnapshot(ctx context.Context, snap Snapshot) error {
	item, err := attributevalue.MarshalMap(snap)
	if err != nil {
		return fmt.Errorf("marshal snapshot gw%d: %w", snap.GW, err)
	}
	item["pk"] = &types.AttributeValueMemberS{Value: s.pk()}
	item["sk"] = &types.AttributeValueMemberS{Value: "SNAPSHOT#GW#" + gwKey(snap.GW)}

	if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item:      item,
	}); err != nil {
		return fmt.Errorf("put snapshot gw%d: %w", snap.GW, err)
	}
	return nil
}

// LatestSnapshotBefore returns the most recent snapshot strictly before gw —
// the baseline the digest measures movement against. Nil means no baseline yet.
func (s *Store) LatestSnapshotBefore(ctx context.Context, gw int) (*Snapshot, error) {
	if gw <= 1 {
		return nil, nil
	}

	out, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.table),
		KeyConditionExpression: aws.String("pk = :pk AND sk BETWEEN :lo AND :hi"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: s.pk()},
			":lo": &types.AttributeValueMemberS{Value: "SNAPSHOT#GW#00"},
			":hi": &types.AttributeValueMemberS{Value: "SNAPSHOT#GW#" + gwKey(gw-1)},
		},
		ScanIndexForward: aws.Bool(false),
		Limit:            aws.Int32(1),
	})
	if err != nil {
		return nil, fmt.Errorf("query snapshot before gw%d: %w", gw, err)
	}
	if len(out.Items) == 0 {
		return nil, nil
	}

	var snap Snapshot
	if err := attributevalue.UnmarshalMap(out.Items[0], &snap); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return &snap, nil
}
