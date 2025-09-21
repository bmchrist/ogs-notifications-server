# OGS Notifications Server - Architecture Documentation

## System Overview

The OGS Notifications Server is a Go-based service that bridges Online-Go.com's game state with iOS push notifications. It continuously monitors game states and delivers timely notifications when players have new turns.

## Core Architecture Principles

### Event-Driven Monitoring
- **Polling-Based**: Uses periodic HTTP requests to OGS API (every 5 minutes)
- **Timestamp Comparison**: Compares game `last_move` timestamps against last notification time
- **Stateful**: Tracks when each device was last notified to prevent duplicates

### Push-First Design
- **Proactive**: Server initiates all communication (no client polling)
- **Consolidated**: Groups multiple new turns into single notifications
- **Reliable**: Uses Apple's APNs infrastructure for guaranteed delivery

## System Components

### 1. HTTP Server (`main.go`)
```
gorilla/mux Router
├── POST /register/:user_id    (Device registration)
└── GET  /check/:user_id       (Manual checking)
```

**Key Functions:**
- `registerDeviceToken()`: Stores iOS device tokens per user
- `getUserTurnStatus()`: Fetches and analyzes game state from OGS
- `sendConsolidatedPushNotification()`: Delivers notifications via APNs

### 2. Periodic Checker
```go
func startPeriodicChecking() {
    ticker := time.NewTicker(checkInterval)
    go func() {
        for range ticker.C {
            checkAllUsers()
        }
    }()
}
```

**Behavior:**
- Runs in background goroutine every 5 minutes
- Checks all registered users sequentially
- For each user: fetches games, identifies "your turn" games, compares `last_move` vs last notification time
- Sends notification only if new moves detected since last notification
- Updates last notification timestamp after successful notification

### 3. OGS API Integration

**Primary Endpoint:** `GET /api/v1/players/{id}/full`

**Data Flow:**
```
OGS API Response → Filter "Your Turn" Games → Compare last_move vs last_notification → Send Notification → Update Timestamp
```

**Key Fields Used:**
- `active_games[].clock.current_player`: Determines whose turn it is
- `active_games[].last_move`: Unix timestamp of last move (compared against last notification time)
- `active_games[].id`: Game identifier for deep linking

**Notification Logic:**
1. Filter games where `clock.current_player` == user ID (your turn)
2. For each "your turn" game, compare `last_move` timestamp vs stored `last_notification_time`
3. If `last_move > last_notification_time`, include in notification
4. Send consolidated notification for all qualifying games
5. Update `last_notification_time` to current timestamp

### 4. APNs Integration

**Authentication:** Token-based (.p8) authentication
```go
client := apns2.NewTokenClient(token)
notification := &apns2.Notification{
    DeviceToken: deviceToken,
    Topic:       bundleID,
    Payload:     payload,
}
```

**Payload Structure:**
```json
{
  "aps": {
    "alert": "You have X new turns in Go games!",
    "sound": "default"
  },
  "web_url": "https://online-go.com/game/12345",
  "app_url": "ogs://game/12345"
}
```

### 5. Persistent Storage (`moves.json`)

**Schema:**
```json
{
  "device_tokens": {
    "user_id": "ios_device_token"
  },
  "last_notification_time": {
    "user_id": unix_timestamp_of_last_notification
  }
}
```

**Thread Safety:** Protected by `sync.Mutex` for concurrent access

## Data Flow Architecture

### Registration Flow
```
iOS App → POST /register/:user_id → Store device token + user ID → moves.json
```

### Monitoring Flow
```
5-Minute Timer → Check All Users → OGS API → Filter "Your Turn" → Compare last_move vs last_notification → APNs → Update Timestamp
```

### Detailed Monitoring Sequence
1. **Timer Trigger**: Every 5 minutes
2. **User Iteration**: For each registered device token
3. **API Call**: Fetch user's active games from OGS (`/api/v1/players/{id}/full`)
4. **Game Filtering**: Identify games where `clock.current_player` == user ID
5. **Timestamp Comparison**: Compare each game's `last_move` vs stored `last_notification_time`
6. **Notification Decision**: If any `last_move > last_notification_time`, prepare notification
7. **Push Delivery**: Send consolidated notification for all qualifying games
8. **State Update**: Update `last_notification_time` to current unix timestamp

## Technical Decisions

### Why Polling vs. Webhooks?
- **OGS Limitation**: No webhook support available
- **Reliability**: Polling ensures we control the checking schedule
- **Simplicity**: No need for webhook endpoint security/validation

### Why Consolidated Notifications?
- **User Experience**: Prevents notification spam
- **Rate Limiting**: Reduces APNs request volume
- **Relevance**: Single actionable notification per check cycle

### Why .p8 vs .p12 Authentication?
- **Apple Recommendation**: Token-based auth is preferred
- **Simplicity**: No password management required
- **Reliability**: Tokens don't expire like certificates

### Why JSON File vs. Database?
- **Scope**: Small user base, simple data structure
- **Deployment**: No external dependencies
- **Development Speed**: Immediate read/write capability

## Configuration Management

### Environment Variables
```bash
# APNs Configuration
APNS_KEY_PATH=AuthKey_A698GDHU6A.p8
APNS_KEY_ID=A698GDHU6A
APNS_TEAM_ID=7GNARLCG65
APNS_BUNDLE_ID=online-go-server-push-notification
APNS_DEVELOPMENT=true

# Operational Settings
CHECK_INTERVAL_MINUTES=3
```

### Runtime Configuration
- **Port**: Hardcoded to 8080
- **Timeouts**: Default HTTP client timeouts
- **Concurrency**: Single-threaded checking (sequential users)

## Error Handling Strategy

### OGS API Failures
- **Strategy**: Log error, continue to next user
- **Rationale**: One user's API issue shouldn't block others

### APNs Failures
- **Strategy**: Log error, continue processing
- **Rationale**: Temporary APNs issues shouldn't stop monitoring

### Storage Failures
- **Strategy**: Log error, attempt to continue
- **Rationale**: Memory state can persist temporarily

## Performance Characteristics

### Scalability Limits
- **Users**: Limited by sequential checking approach (~100 users per minute)
- **Memory**: JSON file loads entirely into memory
- **Network**: One OGS API call per user per check cycle

### Optimization Opportunities
1. **Parallel Checking**: Goroutines per user check
2. **Database Migration**: For larger user bases
3. **Caching**: OGS API response caching
4. **Webhooks**: If OGS adds webhook support

## Security Considerations

### APNs Security
- **Token Storage**: .p8 key stored as file (should be environment variable in production)
- **Device Tokens**: Stored in plaintext (acceptable for push tokens)
- **TLS**: All APNs communication over TLS

### API Security
- **OGS API**: Public endpoints, no authentication required
- **Input Validation**: User IDs validated as integers
- **Rate Limiting**: None implemented (relies on OGS API rate limits)

## Deployment Architecture

### Current Setup
```
Local Development Environment
├── Go Binary (./ogs-server)
├── Configuration (.env file)
├── APNs Key (AuthKey_A698GDHU6A.p8)
└── Storage (moves.json)
```

### Production Considerations
1. **Process Management**: systemd/supervisor for auto-restart
2. **Logging**: Structured logging with rotation
3. **Monitoring**: Health checks and alerting
4. **Secrets**: Environment-based secret management
5. **Backup**: Regular moves.json backups

## Integration Points

### iOS App Requirements
- **Bundle ID**: Must match `APNS_BUNDLE_ID`
- **URL Schemes**: Handle both `ogs://` and `https://online-go.com`
- **Device Token**: 64-character hex string from APNs registration

### OGS Dependencies
- **API Stability**: Relies on `/api/v1/players/{id}/full` endpoint
- **Data Format**: Expects specific JSON structure for games
- **Rate Limits**: Must respect OGS API rate limiting

## Future Architecture Considerations

### Horizontal Scaling
- **Database**: Move to PostgreSQL/MongoDB for user data
- **Queue System**: Redis/RabbitMQ for async processing
- **Load Balancing**: Multiple server instances

### Feature Extensions
- **Multi-Platform**: Android push notification support
- **Custom Intervals**: Per-user check frequency
- **Game Filtering**: Notification preferences per game type
- **Analytics**: Usage tracking and metrics

### Reliability Improvements
- **Circuit Breakers**: For OGS API failures
- **Retry Logic**: Exponential backoff for failed operations
- **Health Endpoints**: For monitoring and alerting