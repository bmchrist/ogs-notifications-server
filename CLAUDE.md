# Claude Agent Coordination - OGS Notifications

This document provides context for Claude agents working on the iOS app at `/Users/ben/projects/ogs-notifications-app`.

## Server Overview

The OGS Notifications Server is a Go application that monitors Online-Go.com games and sends push notifications to iOS devices. It runs continuously and automatically checks for new turns every 30 seconds, tracking when each device was last notified to prevent duplicate notifications.

## Core Notification Logic

**Key Principle**: Only notify when `game.last_move > user.last_notification_time`

1. **Every 30 seconds**: Check all registered users
2. **For each user**: Fetch active games from OGS API (`/api/v1/players/{id}/full`)
3. **Filter games**: Where `clock.current_player == user_id` (your turn)
4. **Compare timestamps**: If any game's `last_move` > stored `last_notification_time`, prepare notification
5. **Send notification**: Consolidated message for all qualifying games
6. **Update timestamp**: Set `last_notification_time` to current unix timestamp

## Server Endpoints for iOS Integration

### Device Registration
```
POST /register
Content-Type: application/json

{
  "user_id": "your_ogs_user_id",
  "device_token": "64-character-hex-device-token"
}
```
- **Purpose**: Register an iOS device to receive notifications for a specific OGS user
- **iOS Implementation**: Call this when user logs in or grants notification permissions
- **User ID**: Can be obtained from OGS profile URL (e.g., `https://online-go.com/user/view/1783478`)
- **Server Behavior**: Stores device token and begins monitoring this user every 30 seconds

### Manual Check (Optional)
```
GET /check/:user_id
```
- **Purpose**: Manually trigger turn checking (primarily for testing)
- **Note**: Not needed in normal operation since server auto-checks every 30 seconds

### User Diagnostics
```
GET /diagnostics/:user_id
```
- **Purpose**: Get comprehensive diagnostics for debugging and user information display
- **iOS Implementation**: Call this to show user status, game information, and server health
- **Returns**: JSON with user status, monitored games, last notification time, and server info

### Find Users by Device Token (New!)
```
GET /users-by-token/:device_token
```
- **Purpose**: Find all user IDs registered to a specific device token
- **iOS Implementation**: Call this on app launch to discover which OGS users are monitored on this device
- **Use Case**: Multi-user support, switching between monitored accounts
- **Returns**: JSON with device token and array of user IDs

#### Users by Device Token Response Format
```json
{
  "device_token": "808605752f2f8d4c4c37e9457668f80cdf20b4d2463c93d00fc7a4698e16afab67a8c99118c296a5177fb7e796063a09eb55f40e269612bfc7f5055069bf73b68d2b774d85e53b96a6b9545f5dcc866a",
  "user_ids": ["1783478", "123456"]
}
```

#### Diagnostics Response Format
```json
{
  "user_id": "1783478",
  "device_token_registered": true,
  "device_token_preview": "808605752f2f8d4c...",
  "last_notification_time": 1758474790961,
  "monitored_games": [
    {
      "game_id": 79504463,
      "last_move_timestamp": 1758474319701,
      "current_player": 1783478,
      "is_your_turn": true,
      "game_name": "test game"
    }
  ],
  "total_active_games": 10,
  "server_check_interval": "30s",
  "last_server_check_time": 1758475925
}
```

**Key Fields for iOS App:**
- `device_token_registered`: Whether this user has a registered device
- `last_notification_time`: Unix timestamp of last notification sent (0 if never)
- `monitored_games[]`: Array of all active games with turn status
- `is_your_turn`: Boolean indicating if it's currently the user's turn
- `last_move_timestamp`: Unix timestamp of last move in each game
- `server_check_interval`: How often server checks for updates
- `total_active_games`: Total number of games being monitored

## Push Notification Payload

The server sends notifications with this structure:
```json
{
  "aps": {
    "alert": "[dev] It's your turn in: Game Name" // Environment prefix in body
  },
  "web_url": "https://online-go.com/game/12345678",
  "app_url": "ogs://game/12345678",
  "game_id": 12345678,
  "action": "open_game",
  "game_name": "Game Name"
}
```

**Environment in Notification Body:**
- Development: `[dev] It's your turn in: Game Name`
- Production: `It's your turn in: Game Name` (no prefix if environment is "none")
- Custom: `[staging] It's your turn in 3 games`

## iOS App Requirements

### 1. APNs Configuration
- **Bundle ID**: Must match server's `APNS_BUNDLE_ID` environment variable
- **Current Bundle ID**: `online-go-server-push-notification`
- **APNs Key**: Server uses .p8 token authentication (Key ID: A698GDHU6A, Team ID: 7GNARLCG65)

### 2. Device Token Handling
```swift
// Pseudo-code for iOS implementation
func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
    let tokenString = deviceToken.map { String(format: "%02.2hhx", $0) }.joined()
    registerWithServer(userID: currentUserID, deviceToken: tokenString)
}

func registerWithServer(userID: String, deviceToken: String) {
    let url = URL(string: "http://YOUR_SERVER:8080/register")!
    let payload = ["user_id": userID, "device_token": deviceToken]
    // Make POST request
}
```

### 3. Deep Link Handling
Handle both URL schemes in your app:
- **Web URLs**: `https://online-go.com/game/12345678` (fallback)
- **App URLs**: `ogs://game/12345678` (preferred for native app experience)

```swift
// Handle app URL scheme
func application(_ app: UIApplication, open url: URL, options: [UIApplication.OpenURLOptionsKey : Any] = [:]) -> Bool {
    if url.scheme == "ogs" && url.host == "game" {
        let gameID = url.lastPathComponent
        // Navigate to game view with gameID
        return true
    }
    return false
}
```

### 4. Notification Interaction
```swift
// Handle notification taps
func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse, withCompletionHandler completionHandler: @escaping () -> Void) {
    let userInfo = response.notification.request.content.userInfo

    if let appURL = userInfo["app_url"] as? String,
       let url = URL(string: appURL) {
        // Handle app URL (preferred)
        handleDeepLink(url)
    } else if let webURL = userInfo["web_url"] as? String,
              let url = URL(string: webURL) {
        // Fallback to web URL
        UIApplication.shared.open(url)
    }

    completionHandler()
}
```

## Server Configuration

### Current Settings
- **Port**: 8080
- **Check Interval**: 30 seconds (configurable via `CHECK_INTERVAL_SECONDS`)
- **Environment**: Development (APNS_DEVELOPMENT=true)
- **Data Storage**: `moves.json` file for persistence

### Server Status
- ✅ APNs integration working with .p8 authentication
- ✅ Automatic periodic checking every 30 seconds
- ✅ Timestamp-based notification logic (last_move vs last_notification_time)
- ✅ Consolidated notifications (prevents spam)
- ✅ Deep linking support
- ✅ Duplicate notification prevention via timestamp tracking

## Testing Workflow

1. **Start Server**: `./ogs-server` (runs continuously)
2. **Register Device**: POST to `/register` with user_id and device_token in JSON body
3. **Verify Registration**: Check server logs for "registered device token"
4. **Wait for Auto-Check**: Server checks every 30 seconds automatically
5. **Make Move on OGS**: Create a new turn situation (opponent moves, now your turn)
6. **Receive Notification**: Should arrive within 30 seconds if `last_move > last_notification_time`

## Common Integration Issues

### Device Token Format
- Must be 64-character hexadecimal string
- Remove spaces and angle brackets from raw device token data

### Bundle ID Mismatch
- iOS app bundle ID must exactly match server's `APNS_BUNDLE_ID`
- Current required value: `online-go-server-push-notification`

### Notification Permissions
- Request permission before registering device token
- Handle permission denial gracefully

### Deep Link Registration
- Register both `ogs://` and `https://online-go.com` URL schemes
- Test deep links work from both notification taps and direct URL opens

## Server Logs to Monitor

```
2025/09/21 09:32:10 APNs client initialized for development
2025/09/21 09:32:10 Starting periodic turn checking every 5m0s
2025/09/21 09:32:41 Checking user 1783478: 2 games need notification (last_move > last_notification_time)
2025/09/21 09:32:41 Push notification sent successfully to user 1783478 for 2 game(s)
2025/09/21 09:32:41 Updated last_notification_time for user 1783478
```

## Files for Reference

- **Server Code**: `/Users/ben/projects/ogs-notifications-server/main.go`
- **Environment Config**: `/Users/ben/projects/ogs-notifications-server/.env.example`
- **Storage**: `/Users/ben/projects/ogs-notifications-server/moves.json`
- **APNs Key**: `/Users/ben/projects/ogs-notifications-server/AuthKey_A698GDHU6A.p8`

## iOS App Implementation Guide

### Essential Features to Implement

1. **Device Registration**: Call `/register` with user_id and device_token on app launch
2. **Push Notification Handling**: Implement notification reception and deep linking
3. **Diagnostics Display**: Use `/diagnostics/:user_id` for user status screen
4. **Deep Link Handling**: Support `ogs://game/:id` URL scheme

### Diagnostics Screen Implementation

Use the diagnostics endpoint to create a user status screen showing:

```swift
struct UserDiagnostics {
    let userId: String
    let deviceTokenRegistered: Bool
    let lastNotificationTime: TimeInterval
    let monitoredGames: [GameInfo]
    let totalActiveGames: Int
    let serverCheckInterval: String
}

struct GameInfo {
    let gameId: Int
    let gameName: String
    let isYourTurn: Bool
    let lastMoveTimestamp: TimeInterval
    let currentPlayer: Int
}
```

**Sample UI Elements:**
- ✅/❌ Device registration status
- 🕐 "Last notified: 2 minutes ago" (or "Never")
- 📊 "Monitoring 10 active games"
- 📋 List of games with turn indicators
- 🔄 "Server checks every 30 seconds"
- 🔗 Deep link buttons to games where it's your turn

### Recommended User Flow

1. **Registration**: Auto-register device token on first launch
2. **Permissions**: Request notification permissions with clear explanation
3. **Diagnostics**: Show status screen for troubleshooting
4. **Notifications**: Handle both direct game links and app-based navigation
5. **Refresh**: Allow manual diagnostics refresh for debugging

## Next Steps for iOS Development

1. Set up basic iOS app with push notification capabilities
2. Implement device token registration with server
3. Add deep link handling for `ogs://game/:id` URLs
4. **Implement diagnostics screen using new endpoint**
5. Test notification reception and interaction
6. Implement OGS user authentication/ID retrieval
7. Add notification settings/preferences UI