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

## API Key Authentication (NEW!)

All protected endpoints now require API key authentication following Apple's security best practices.

### Obtaining an API Key
```
POST /generate-api-key
Content-Type: application/json

{
  "user_id": "your_ogs_user_id",
  "master_key": "master_key_from_server_admin",
  "description": "iOS App - iPhone 15 Pro"
}
```

**Response:**
```json
{
  "api_key": "64-character-hex-api-key",
  "user_id": "1783478",
  "created_at": "2025-09-22T14:30:00Z",
  "description": "iOS App - iPhone 15 Pro"
}
```

### Using the API Key

All protected endpoints require the API key in the `X-API-Key` header:
```
X-API-Key: your-64-character-api-key
```

### iOS Implementation Guidelines

#### Secure Storage with Keychain

**IMPORTANT**: Never store API keys in UserDefaults, plist files, or hardcode them. Use iOS Keychain for secure storage.

```swift
import Security

class APIKeyManager {
    static let shared = APIKeyManager()
    private let keychainKey = "com.ogs.notifications.apikey"
    private let keychainAccessGroup = "your.app.group" // Optional for app groups

    // Save API key to Keychain
    func saveAPIKey(_ apiKey: String) -> Bool {
        guard let data = apiKey.data(using: .utf8) else { return false }

        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrAccount as String: keychainKey,
            kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        ]

        // Delete any existing key
        SecItemDelete(query as CFDictionary)

        // Add new key
        let status = SecItemAdd(query as CFDictionary, nil)
        return status == errSecSuccess
    }

    // Retrieve API key from Keychain
    func getAPIKey() -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrAccount as String: keychainKey,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne
        ]

        var dataTypeRef: AnyObject?
        let status = SecItemCopyMatching(query as CFDictionary, &dataTypeRef)

        guard status == errSecSuccess,
              let data = dataTypeRef as? Data,
              let apiKey = String(data: data, encoding: .utf8) else {
            return nil
        }

        return apiKey
    }

    // Delete API key from Keychain
    func deleteAPIKey() -> Bool {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrAccount as String: keychainKey
        ]

        let status = SecItemDelete(query as CFDictionary)
        return status == errSecSuccess || status == errSecItemNotFound
    }
}
```

#### Network Requests with Authentication

```swift
class OGSNotificationService {
    private let baseURL = "https://your-server.com"

    func makeAuthenticatedRequest(endpoint: String, method: String = "GET", body: Data? = nil) async throws -> Data {
        guard let apiKey = APIKeyManager.shared.getAPIKey() else {
            throw APIError.noAPIKey
        }

        guard let url = URL(string: "\(baseURL)\(endpoint)") else {
            throw APIError.invalidURL
        }

        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue(apiKey, forHTTPHeaderField: "X-API-Key")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")

        if let body = body {
            request.httpBody = body
        }

        let (data, response) = try await URLSession.shared.data(for: request)

        guard let httpResponse = response as? HTTPURLResponse else {
            throw APIError.invalidResponse
        }

        switch httpResponse.statusCode {
        case 200...299:
            return data
        case 401:
            throw APIError.unauthorized
        case 403:
            throw APIError.forbidden
        default:
            throw APIError.serverError(httpResponse.statusCode)
        }
    }

    // Example: Register device
    func registerDevice(userID: String, deviceToken: String) async throws {
        let payload = [
            "user_id": userID,
            "device_token": deviceToken
        ]

        let jsonData = try JSONSerialization.data(withJSONObject: payload)
        _ = try await makeAuthenticatedRequest(endpoint: "/register", method: "POST", body: jsonData)
    }

    // Example: Get diagnostics
    func getDiagnostics(for userID: String) async throws -> UserDiagnostics {
        let data = try await makeAuthenticatedRequest(endpoint: "/diagnostics/\(userID)")
        return try JSONDecoder().decode(UserDiagnostics.self, from: data)
    }
}

enum APIError: LocalizedError {
    case noAPIKey
    case invalidURL
    case invalidResponse
    case unauthorized
    case forbidden
    case serverError(Int)

    var errorDescription: String? {
        switch self {
        case .noAPIKey:
            return "No API key found. Please log in again."
        case .unauthorized:
            return "Invalid API key. Please log in again."
        case .forbidden:
            return "Access forbidden. Please check your permissions."
        case .serverError(let code):
            return "Server error (\(code)). Please try again."
        default:
            return "An error occurred. Please try again."
        }
    }
}
```

#### Initial Setup Flow

1. **First Launch**: Check Keychain for existing API key
2. **No API Key Found**: Show login/setup screen
3. **User Provides Credentials**:
   - For new users: Admin generates API key via `/generate-api-key` endpoint
   - Provide API key to user through secure channel
4. **Store API Key**: Save to Keychain using `APIKeyManager`
5. **Register Device**: Call `/register` endpoint with API key
6. **Begin Monitoring**: Server starts checking for turns

#### Security Best Practices

1. **Never expose the master key**: Keep it secure on the server
2. **Use HTTPS only**: Never send API keys over unencrypted connections
3. **Implement key rotation**: Allow users to regenerate keys if compromised
4. **Add biometric protection**: Use Face ID/Touch ID for additional security
5. **Clear on logout**: Delete API key from Keychain when user logs out
6. **Handle 401 errors**: Prompt re-authentication on unauthorized responses

```swift
// Biometric protection example
import LocalAuthentication

func authenticateWithBiometrics(completion: @escaping (Bool) -> Void) {
    let context = LAContext()
    var error: NSError?

    if context.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: &error) {
        context.evaluatePolicy(.deviceOwnerAuthenticationWithBiometrics,
                              localizedReason: "Authenticate to access OGS notifications") { success, _ in
            DispatchQueue.main.async {
                completion(success)
            }
        }
    } else {
        completion(false)
    }
}
```

## Server Endpoints for iOS Integration

### Device Registration (Protected)
```
POST /register
Content-Type: application/json
X-API-Key: your-api-key

{
  "user_id": "your_ogs_user_id",
  "device_token": "64-character-hex-device-token"
}
```
- **Purpose**: Register an iOS device to receive notifications for a specific OGS user
- **iOS Implementation**: Call this when user logs in or grants notification permissions
- **User ID**: Can be obtained from OGS profile URL (e.g., `https://online-go.com/user/view/1783478`)
- **Server Behavior**: Stores device token and begins monitoring this user every 30 seconds

### Manual Check (Protected)
```
GET /check/:user_id
X-API-Key: your-api-key
```
- **Purpose**: Manually trigger turn checking (primarily for testing)
- **Note**: Not needed in normal operation since server auto-checks every 30 seconds

### User Diagnostics (Protected)
```
GET /diagnostics/:user_id
X-API-Key: your-api-key
```
- **Purpose**: Get comprehensive diagnostics for debugging and user information display
- **iOS Implementation**: Call this to show user status, game information, and server health
- **Returns**: JSON with user status, monitored games, last notification time, and server info

### Find Users by Device Token (Protected)
```
GET /users-by-token/:device_token
X-API-Key: your-api-key
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