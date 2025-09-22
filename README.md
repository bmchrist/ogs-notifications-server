# OGS Notifications Server

A Go server that monitors [Online-Go.com](https://online-go.com) games and sends iOS push notifications when it's your turn to play.

## Features

- **Automatic Turn Monitoring**: Continuously checks your OGS games every few minutes
- **iOS Push Notifications**: Sends consolidated notifications when new turns are detected
- **Deep Linking**: Notifications include both web URLs and iOS app URLs for seamless game access
- **Smart Notifications**: Combines multiple new turns into a single notification to avoid spam
- **Persistent Tracking**: Remembers which moves you've already been notified about
- **API Key Authentication**: Secure API key-based authentication for all endpoints

## Quick Start

### Prerequisites

- Go 1.19 or later
- Apple Developer account with push notification capabilities
- APNs authentication key (.p8 file) from Apple Developer portal

### Setup

1. **Clone and build:**
   ```bash
   git clone <repository-url>
   cd ogs-notifications-server
   go mod download
   go build -o ogs-server
   ```

2. **Configure environment variables:**
   ```bash
   cp .env.example .env
   # Edit .env with your APNs credentials
   ```

3. **Required environment variables:**
   - `APNS_KEY_PATH`: Path to your .p8 authentication key file
   - `APNS_KEY_ID`: Your APNs key ID (10 characters)
   - `APNS_TEAM_ID`: Your Apple Developer team ID (10 characters)
   - `APNS_BUNDLE_ID`: Your iOS app's bundle identifier
   - `APNS_DEVELOPMENT`: Set to `true` for development, `false` for production
   - `CHECK_INTERVAL_MINUTES`: How often to check for new turns (default: 3)
   - `ENVIRONMENT`: Deployment environment name (optional, defaults to "none")
   - `MASTER_API_KEY`: Master key for generating API keys (auto-generated if not set)

### Running the Server

```bash
# Load environment and start server
source .env
./ogs-server
```

The server will:
- Start on port 8080
- Begin checking all registered users every few minutes
- Send push notifications automatically when new turns are detected

## API Authentication

All protected endpoints require API key authentication using the `X-API-Key` header.

### Generate API Key

To generate an API key for a user:

```bash
POST /generate-api-key
Content-Type: application/json

{
  "user_id": "your_ogs_user_id",
  "master_key": "master_key_from_server_logs",
  "description": "iOS App - Device Name"
}
```

Response:
```json
{
  "api_key": "64-character-hex-api-key",
  "user_id": "1783478",
  "created_at": "2025-09-22T14:30:00Z",
  "description": "iOS App - Device Name"
}
```

**Note**: The master key is logged when the server starts. Set `MASTER_API_KEY` environment variable to use a persistent key.

## API Endpoints

All endpoints below require the `X-API-Key` header for authentication.

### Register a Device Token (Protected)

```bash
POST /register
Content-Type: application/json
X-API-Key: your-api-key

{
  "user_id": "your_ogs_user_id",
  "device_token": "your_ios_device_token_here"
}
```

### Manual Turn Check (Protected)

```bash
GET /check/:user_id
X-API-Key: your-api-key
```

Returns JSON with current game status and sends notifications if needed.

### User Diagnostics (Protected)

```bash
GET /diagnostics/:user_id
X-API-Key: your-api-key
```

Returns comprehensive user status including device registration, monitored games, and last notification time.

### Find Users by Device Token (Protected)

```bash
GET /users-by-token/:device_token
X-API-Key: your-api-key
```

Returns all user IDs that are registered to a specific device token. Useful for iOS apps to discover which OGS users are monitored on the current device.

## Getting Your OGS User ID

1. Go to your profile on [Online-Go.com](https://online-go.com)
2. Your user ID is the number in the URL: `https://online-go.com/user/view/YOUR_ID_HERE`

## Getting APNs Credentials

1. Log into [Apple Developer portal](https://developer.apple.com)
2. Go to Certificates, Identifiers & Profiles
3. Create an APNs authentication key (.p8 file)
4. Note your Key ID and Team ID for configuration

## Notification Behavior

- **Single Game**: "You have a new turn in Go Game!"
- **Multiple Games**: "You have 3 new turns in Go games!"
- Each notification includes a deep link to one of the games
- Only sends notifications for newly detected turns (not existing ones)

## Storage

The server uses `moves.json` to persist:
- Move timestamps for each user's games (prevents duplicate notifications)
- Device token registrations

## Troubleshooting

### "MissingProviderToken" Error
- Ensure you're using .p8 token authentication (not .p12 certificates)
- Verify your bundle ID matches your iOS app exactly

### No Notifications Received
- Check that your device token is correctly registered
- Verify APNs credentials are valid
- Ensure your iOS app has push notification permissions

### Port Already in Use
```bash
# Kill existing server instances
pkill -f ogs-server
```

## Development

### Testing with Shorter Intervals
```bash
CHECK_INTERVAL_MINUTES=1 ./ogs-server
```

### Manual Testing
```bash
curl -X POST http://localhost:8080/register \
  -H "Content-Type: application/json" \
  -d '{"user_id": "YOUR_USER_ID", "device_token": "YOUR_DEVICE_TOKEN"}'

curl http://localhost:8080/check/YOUR_USER_ID
```

## License

MIT License - see LICENSE file for details.