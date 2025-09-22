# GCP Deployment Guide

This guide covers deploying the OGS Notifications Server to Google Cloud Platform using Secret Manager for production credentials.

## Overview

The server automatically detects the environment:
- **Development**: Uses environment variables and local files
- **Production**: Uses Google Secret Manager for sensitive data

## Prerequisites

1. **Google Cloud Project**: Create or select a GCP project
2. **Enable APIs**:
   ```bash
   gcloud services enable secretmanager.googleapis.com
   gcloud services enable run.googleapis.com
   gcloud services enable cloudbuild.googleapis.com
   ```

3. **APNs Credentials**: Your .p8 key file and associated IDs from Apple Developer

## Step 1: Set Up Secrets

Upload your APNs credentials to Secret Manager:

```bash
# Set your project ID
export PROJECT_ID="your-project-id"
gcloud config set project $PROJECT_ID

# Create secrets for APNs configuration
gcloud secrets create apns-key --data-file=AuthKey_A698GDHU6A.p8 --replication-policy=automatic
gcloud secrets create apns-key-id --data-file=- <<< "A698GDHU6A"
gcloud secrets create apns-team-id --data-file=- <<< "7GNARLCG65"
gcloud secrets create apns-bundle-id --data-file=- <<< "online-go-server-push-notification"

# Verify secrets were created
gcloud secrets list
```

## Step 2: Create Service Account

Create a service account with minimal required permissions:

```bash
# Create service account
gcloud iam service-accounts create ogs-notifications \
    --display-name="OGS Notifications Server" \
    --description="Service account for OGS notifications server"

# Grant Secret Manager access
gcloud projects add-iam-policy-binding $PROJECT_ID \
    --member="serviceAccount:ogs-notifications@$PROJECT_ID.iam.gserviceaccount.com" \
    --role="roles/secretmanager.secretAccessor"
```

## Step 3: Build and Deploy

### Option A: Using Cloud Build

1. **Create Dockerfile:**
```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o ogs-server

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/ogs-server .
EXPOSE 8080
CMD ["./ogs-server"]
```

2. **Deploy with Cloud Build:**
```bash
# Build and deploy to Cloud Run
gcloud run deploy ogs-notifications \
    --source . \
    --platform managed \
    --region us-central1 \
    --service-account ogs-notifications@$PROJECT_ID.iam.gserviceaccount.com \
    --set-env-vars="ENVIRONMENT=production,CHECK_INTERVAL_SECONDS=30,GOOGLE_CLOUD_PROJECT=$PROJECT_ID" \
    --allow-unauthenticated \
    --memory=512Mi \
    --cpu=1 \
    --timeout=3600 \
    --max-instances=10
```

### Option B: Manual Docker Build

```bash
# Build locally and push to Container Registry
docker build -t gcr.io/$PROJECT_ID/ogs-notifications .
docker push gcr.io/$PROJECT_ID/ogs-notifications

# Deploy to Cloud Run
gcloud run deploy ogs-notifications \
    --image gcr.io/$PROJECT_ID/ogs-notifications \
    --platform managed \
    --region us-central1 \
    --service-account ogs-notifications@$PROJECT_ID.iam.gserviceaccount.com \
    --set-env-vars="ENVIRONMENT=production,CHECK_INTERVAL_SECONDS=30,GOOGLE_CLOUD_PROJECT=$PROJECT_ID" \
    --allow-unauthenticated \
    --memory=512Mi \
    --cpu=1 \
    --timeout=3600 \
    --max-instances=10
```

## Step 4: Verify Deployment

1. **Check the service is running:**
```bash
# Get the service URL
SERVICE_URL=$(gcloud run services describe ogs-notifications --region=us-central1 --format='value(status.url)')
echo "Service URL: $SERVICE_URL"

# Test health endpoint
curl $SERVICE_URL/health
```

2. **Check logs:**
```bash
gcloud run services logs read ogs-notifications --region=us-central1
```

You should see:
```
Loading APNs configuration from Secret Manager...
APNs configuration loaded from Secret Manager
APNs client initialized for production
Server starting on :8080
Automatic turn checking enabled
```

## Environment Configuration

The server behavior is controlled by the `ENVIRONMENT` variable:

| Environment | APNs Source | APNs Mode | Bundle ID Source |
|-------------|-------------|-----------|------------------|
| `production` | Secret Manager | Production | Secret Manager |
| `dev` or other | Environment Variables | Development | Environment Variables |

### Required Environment Variables for Production

- `ENVIRONMENT=production` (triggers Secret Manager usage)
- `GOOGLE_CLOUD_PROJECT=your-project-id` (for Secret Manager access)
- `CHECK_INTERVAL_SECONDS=30` (optional, defaults to 30)

### Required Secrets in Secret Manager

- `apns-key`: The .p8 key file content
- `apns-key-id`: APNs Key ID (e.g., "A698GDHU6A")
- `apns-team-id`: Apple Developer Team ID (e.g., "7GNARLCG65")
- `apns-bundle-id`: iOS app bundle identifier

## Monitoring and Maintenance

### Viewing Logs
```bash
# Real-time logs
gcloud run services logs tail ogs-notifications --region=us-central1

# Recent logs
gcloud run services logs read ogs-notifications --region=us-central1 --limit=100
```

### Updating Secrets
```bash
# Update a secret (e.g., if you regenerate APNs key)
gcloud secrets versions add apns-key --data-file=NewAuthKey.p8
```

### Scaling
Cloud Run automatically scales based on traffic. Current limits:
- Max instances: 10
- Memory: 512Mi
- CPU: 1
- Timeout: 3600s (1 hour)

## Security Notes

1. **Secret Manager**: All sensitive APNs data is encrypted at rest and in transit
2. **Service Account**: Uses principle of least privilege (only Secret Manager access)
3. **No secrets in container**: APNs keys are never baked into container images
4. **Audit logging**: All secret access is logged by Google Cloud

## Troubleshooting

### Common Issues

1. **"GOOGLE_CLOUD_PROJECT environment variable not set"**
   - Ensure `GOOGLE_CLOUD_PROJECT` is set in Cloud Run environment variables

2. **"failed to access secret"**
   - Verify service account has `roles/secretmanager.secretAccessor`
   - Check that secrets exist: `gcloud secrets list`

3. **"APNs client not initialized"**
   - Check logs for specific error in APNs configuration loading
   - Verify secret content matches expected format

4. **Health check fails**
   - Check if service is allocated enough memory/CPU
   - Verify container starts successfully: `gcloud run services logs read ogs-notifications`

### Testing Locally with Production Config

To test Secret Manager integration locally:

```bash
# Authenticate with your user account
gcloud auth application-default login

# Run with production environment
ENVIRONMENT=production GOOGLE_CLOUD_PROJECT=your-project-id ./ogs-server
```

## Cost Optimization

- **Secret Manager**: ~$0.06 per 10,000 operations
- **Cloud Run**: Pay per request + CPU/memory usage
- **Consider**: Use Cloud Run minimum instances (1) for consistent availability

## Updates and Rollbacks

```bash
# Deploy new version
gcloud run deploy ogs-notifications --source .

# Rollback to previous version
gcloud run services update-traffic ogs-notifications --to-revisions=PREVIOUS_REVISION=100
```