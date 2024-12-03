# Automatic Update Promotion System

This system automatically promotes updates through channels (dev → beta → stable) based on time and success criteria.

## How it Works

1. **Channels**:
   - `dev`: Initial testing channel
   - `beta`: Pre-release testing
   - `stable`: Production release

2. **Promotion Criteria**:
   - Dev → Beta: After 24 hours in dev channel
   - Beta → Stable: After 72 hours in beta channel with no reported issues

3. **Infrastructure**:
   - S3 bucket for storing updates
   - DynamoDB table for tracking update status
   - Lambda function for handling promotions
   - EventBridge rule for scheduling checks

## Prerequisites

1. Install Node.js and npm:
   ```bash
   brew install node
   ```

2. Install Serverless Framework:
   ```bash
   npm install -g serverless
   ```

3. Configure AWS credentials:
   ```bash
   aws configure
   ```

## Deployment

1. Install Go dependencies:
   ```bash
   cd aws/lambda
   go mod tidy
   ```

2. Deploy using Serverless Framework:
   ```bash
   cd ..
   serverless deploy
   ```

   To deploy to a specific stage:
   ```bash
   serverless deploy --stage prod
   ```

## Usage

1. **Upload a New Version**:
   ```bash
   # Get the bucket name
   export BUCKET_NAME=$(aws cloudformation describe-stacks \
     --stack-name go-selfupdate-dev \
     --query 'Stacks[0].Outputs[?OutputKey==`UpdatesBucketName`].OutputValue' \
     --output text)

   # Upload binary
   aws s3 cp myapp.gz s3://${BUCKET_NAME}/myapp/dev/1.0.0/binary.gz

   # Upload manifest
   cat > version.json << EOF
   {
     "Version": "1.0.0",
     "Sha256": "...",
     "Channel": "dev",
     "Date": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
   }
   EOF
   aws s3 cp version.json s3://${BUCKET_NAME}/myapp/dev/version.json
   ```

2. **Track Promotion Status**:
   ```bash
   # Get the table name
   export TABLE_NAME=$(aws cloudformation describe-stacks \
     --stack-name go-selfupdate-dev \
     --query 'Stacks[0].Outputs[?OutputKey==`UpdateTrackingTableName`].OutputValue' \
     --output text)

   # Check status
   aws dynamodb get-item \
     --table-name ${TABLE_NAME} \
     --key '{"version": {"S": "1.0.0"}}'
   ```

## Promotion Timeline

1. **Dev Channel** (0-24 hours):
   - Initial testing period
   - Automated tests and monitoring

2. **Beta Channel** (24-96 hours):
   - Wider testing audience
   - Monitoring for issues
   - Automatic rollback if issues detected

3. **Stable Channel** (96+ hours):
   - Available to all users
   - Monitored for long-term stability

## Customization

1. Time thresholds in `promote_update.go`:
   ```go
   const (
       minTimeInDev  = 24 * time.Hour
       minTimeInBeta = 72 * time.Hour
   )
   ```

2. Check frequency in `serverless.yml`:
   ```yaml
   functions:
     promoteUpdate:
       events:
         - schedule:
             rate: rate(1 hour)
   ```

## Monitoring

1. **CloudWatch Logs**:
   ```bash
   serverless logs -f promoteUpdate
   ```

2. **DynamoDB**:
   ```bash
   aws dynamodb scan --table-name ${TABLE_NAME}
   ```

3. **S3**:
   ```bash
   aws s3 ls s3://${BUCKET_NAME}/myapp/ --recursive
   ```

## Cleanup

To remove all resources:
```bash
serverless remove
```

## Security Features

- S3 bucket versioning enabled
- Server-side encryption (AES-256)
- Public access blocked
- IAM roles with least privilege
- DynamoDB TTL for old records
- Audit logging via CloudTrail