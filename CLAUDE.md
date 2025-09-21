# Claude Agent Coordination - OGS Notifications

This document provides context for Claude agents working on the iOS app at `/Users/ben/projects/ogs-notifications-app`.

## Server Overview

The OGS Notifications Server is a Go application that monitors Online-Go.com games and sends push notifications to iOS devices. It runs continuously and automatically checks for new turns every 5 minutes, tracking when each device was last notified to prevent duplicate notifications.

## Core Notification Logic

**Key Principle**: Only notify when `game.last_move > user.last_notification_time`

1. **Every 5 minutes**: Check all registered users
2. **For each user**: Fetch active games from OGS API (`/api/v1/players/{id}/full`)
3. **Filter games**: Where `clock.current_player == user_id` (your turn)
4. **Compare timestamps**: If any game's `last_move` > stored `last_notification_time`, prepare notification
5. **Send notification**: Consolidated message for all qualifying games
6. **Update timestamp**: Set `last_notification_time` to current unix timestamp

## Server Endpoints for iOS Integration

### Device Registration
```
POST /register/:user_id
Content-Type: application/json

{
  "device_token": "64-character-hex-device-token"
}
```
- **Purpose**: Register an iOS device to receive notifications for a specific OGS user
- **iOS Implementation**: Call this when user logs in or grants notification permissions
- **User ID**: Can be obtained from OGS profile URL (e.g., `https://online-go.com/user/view/1783478`)
- **Server Behavior**: Stores device token and begins monitoring this user every 5 minutes

### Manual Check (Optional)
```
GET /check/:user_id
```
- **Purpose**: Manually trigger turn checking (primarily for testing)
- **Note**: Not needed in normal operation since server auto-checks every 5 minutes

## Push Notification Payload

The server sends notifications with this structure:
```json
{
  "aps": {
    "alert": "You have a new turn in Go Game!" // or "You have X new turns in Go games!"
  },
  "web_url": "https://online-go.com/game/12345678",
  "app_url": "ogs://game/12345678"
}
```

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
    let url = URL(string: "http://YOUR_SERVER:8080/register/\(userID)")!
    let payload = ["device_token": deviceToken]
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
- **Check Interval**: 5 minutes (configurable via `CHECK_INTERVAL_MINUTES`)
- **Environment**: Development (APNS_DEVELOPMENT=true)
- **Data Storage**: `moves.json` file for persistence

### Server Status
- ✅ APNs integration working with .p8 authentication
- ✅ Automatic periodic checking every 5 minutes
- ✅ Timestamp-based notification logic (last_move vs last_notification_time)
- ✅ Consolidated notifications (prevents spam)
- ✅ Deep linking support
- ✅ Duplicate notification prevention via timestamp tracking

## Testing Workflow

1. **Start Server**: `./ogs-server` (runs continuously)
2. **Register Device**: POST to `/register/:user_id` with device token
3. **Verify Registration**: Check server logs for "registered device token"
4. **Wait for Auto-Check**: Server checks every 5 minutes automatically
5. **Make Move on OGS**: Create a new turn situation (opponent moves, now your turn)
6. **Receive Notification**: Should arrive within 5 minutes if `last_move > last_notification_time`

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

## Next Steps for iOS Development

1. Set up basic iOS app with push notification capabilities
2. Implement device token registration with server
3. Add deep link handling for `ogs://game/:id` URLs
4. Test notification reception and interaction
5. Implement OGS user authentication/ID retrieval
6. Add notification settings/preferences UI