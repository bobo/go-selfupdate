package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	minTimeInDev  = 24 * time.Hour // Minimum time in dev channel
	minTimeInBeta = 72 * time.Hour // Minimum time in beta channel
)

type UpdateInfo struct {
	Version string    `json:"Version"`
	Sha256  []byte    `json:"Sha256"`
	Channel string    `json:"Channel"`
	Date    time.Time `json:"Date"`
}

type UpdateRecord struct {
	Version      string    `dynamodb:"version"`
	Channel      string    `dynamodb:"channel"`
	Date         time.Time `dynamodb:"date"`
	DevApproved  bool      `dynamodb:"dev_approved"`
	BetaApproved bool      `dynamodb:"beta_approved"`
}

type PromoteEvent struct {
	Source string `json:"source"` // "scheduler" for automatic checks
}

func checkAndPromote(ctx context.Context, dynamoClient *dynamodb.Client, s3Client *s3.Client) error {
	// Scan DynamoDB for updates that might be ready for promotion
	result, err := dynamoClient.Scan(ctx, &dynamodb.ScanInput{
		TableName: aws.String("update_tracking"),
	})
	if err != nil {
		return fmt.Errorf("failed to scan DynamoDB: %v", err)
	}

	for _, item := range result.Items {
		var record UpdateRecord
		if err := attributevalue.UnmarshalMap(item, &record); err != nil {
			continue
		}

		// Check if update can be promoted based on channel and time
		switch record.Channel {
		case "dev":
			if time.Since(record.Date) >= minTimeInDev && !record.DevApproved {
				// Promote from dev to beta
				if err := promoteUpdate(ctx, s3Client, dynamoClient, record.Version, "dev", "beta"); err != nil {
					fmt.Printf("Failed to promote %s from dev to beta: %v\n", record.Version, err)
				}
			}
		case "beta":
			if time.Since(record.Date) >= minTimeInBeta && record.DevApproved && !record.BetaApproved {
				// Promote from beta to stable
				if err := promoteUpdate(ctx, s3Client, dynamoClient, record.Version, "beta", "stable"); err != nil {
					fmt.Printf("Failed to promote %s from beta to stable: %v\n", record.Version, err)
				}
			}
		}
	}

	return nil
}

func promoteUpdate(ctx context.Context, s3Client *s3.Client, dynamoClient *dynamodb.Client, version, fromChannel, toChannel string) error {
	bucketName := os.Getenv("BUCKET_NAME")
	if bucketName == "" {
		return fmt.Errorf("BUCKET_NAME environment variable not set")
	}

	// Get the update info from the source channel
	sourceKey := fmt.Sprintf("myapp/%s/%s.json", fromChannel, version)
	result, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(sourceKey),
	})
	if err != nil {
		return fmt.Errorf("failed to get source update: %v", err)
	}

	var updateInfo UpdateInfo
	if err := json.NewDecoder(result.Body).Decode(&updateInfo); err != nil {
		return fmt.Errorf("failed to decode update info: %v", err)
	}

	// Update the channel in the info
	updateInfo.Channel = toChannel
	updateInfo.Date = time.Now()

	// Serialize the updated info
	updateData, err := json.Marshal(updateInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal update info: %v", err)
	}

	// Copy the update files to the new channel
	// 1. Copy the JSON manifest
	destKey := fmt.Sprintf("myapp/%s/%s.json", toChannel, version)
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(destKey),
		Body:   bytes.NewReader(updateData),
	})
	if err != nil {
		return fmt.Errorf("failed to copy manifest: %v", err)
	}

	// 2. Copy the binary
	sourceBinKey := fmt.Sprintf("myapp/%s/%s/binary.gz", fromChannel, version)
	destBinKey := fmt.Sprintf("myapp/%s/%s/binary.gz", toChannel, version)
	_, err = s3Client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(bucketName),
		CopySource: aws.String(fmt.Sprintf("%s/%s", bucketName, sourceBinKey)),
		Key:        aws.String(destBinKey),
	})
	if err != nil {
		return fmt.Errorf("failed to copy binary: %v", err)
	}

	// Update DynamoDB record
	updateExp := "SET "
	expAttr := map[string]types.AttributeValue{}

	if toChannel == "beta" {
		updateExp += "channel = :channel, dev_approved = :true, #date = :date"
		expAttr[":channel"] = &types.AttributeValueMemberS{Value: "beta"}
		expAttr[":true"] = &types.AttributeValueMemberBOOL{Value: true}
		expAttr[":date"] = &types.AttributeValueMemberS{Value: time.Now().Format(time.RFC3339)}
	} else if toChannel == "stable" {
		updateExp += "channel = :channel, beta_approved = :true, #date = :date"
		expAttr[":channel"] = &types.AttributeValueMemberS{Value: "stable"}
		expAttr[":true"] = &types.AttributeValueMemberBOOL{Value: true}
		expAttr[":date"] = &types.AttributeValueMemberS{Value: time.Now().Format(time.RFC3339)}
	}

	_, err = dynamoClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String("update_tracking"),
		Key: map[string]types.AttributeValue{
			"version": &types.AttributeValueMemberS{Value: version},
		},
		UpdateExpression:          aws.String(updateExp),
		ExpressionAttributeValues: expAttr,
		ExpressionAttributeNames: map[string]string{
			"#date": "date",
		},
	})
	if err != nil {
		return fmt.Errorf("failed to update DynamoDB record: %v", err)
	}

	return nil
}

func handleRequest(ctx context.Context, event PromoteEvent) error {
	// Load AWS configuration
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("unable to load SDK config: %v", err)
	}

	// Create S3 and DynamoDB clients
	s3Client := s3.NewFromConfig(cfg)
	dynamoClient := dynamodb.NewFromConfig(cfg)

	// Check and promote updates
	return checkAndPromote(ctx, dynamoClient, s3Client)
}

func main() {
	lambda.Start(handleRequest)
}
