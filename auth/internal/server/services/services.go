// Package services provides the core business logic for authentication,
// authorization, and user/session management.
//
// It acts as an orchestration layer between transport (HTTP/handlers)
// and persistence (repositories), encapsulating all domain-specific
// workflows such as login, registration, token generation, session
// lifecycle, and user data access.
//
// # Overview
//
// The package implements multiple versions of authentication services,
// with ServiceV2 extending the functionality of ServiceV1. The services
// coordinate operations across several repositories:
//
//   - Session repository: manages session persistence and lifecycle
//   - User attributes repository: stores user profile data
//   - User name repository: handles identity creation
//   - User view repository: provides aggregated read models for authentication
//
// All operations are designed to be context-aware and support cancellation,
// deadlines, and structured logging.
//
// # Authentication Flow
//
// The authentication process typically consists of:
//
//  1. Fetching user credentials from the user view repository
//  2. Validating password hashes
//  3. Creating a new session with a unique session identifier
//  4. Generating access and refresh tokens (JWT-based)
//  5. Returning credentials including tokens and metadata
//
// Passwords are hashed and verified using helper utilities from the tool package.
//
// # Token Management
//
// The package uses JWT (HS256) for both access and refresh tokens.
//
//   - Access token:
//     Contains standard claims (exp, iss, jti, sub)
//     Encodes identifiers as plain UUID strings
//
//   - Refresh token:
//     Contains the same claims, but values are base64-encoded
//     Delivered via secure HTTP-only cookie
//
// Token validity periods are configurable and enforced during refresh.
//
// # Session Lifecycle
//
// Sessions are persisted in the database and identified by a composite key
// derived from:
//
//   - Issuer (iss)
//   - Token ID (jti)
//   - Subject (sub)
//
// Key operations:
//
//   - Creation during authentication and registration
//   - Validation during token refresh
//   - Deletion during logout or expiration
//
// Expired sessions are automatically invalidated and removed.
//
// # Transactions
//
// Critical operations (e.g., authentication and registration) are executed
// within database transactions to ensure consistency.
//
// The helper function deferTransaction ensures that:
//
//   - Transactions are committed on success
//   - Transactions are rolled back on error
//   - Errors during commit/rollback are logged
//
// # Error Handling
//
// Errors are classified and normalized using the xerror package,
// allowing consistent handling of:
//
//   - Database errors (e.g., unique violations)
//   - Authentication failures
//   - Token validation issues
//
// Logging is performed using structured logging (log/slog).
//
// # Extensibility
//
// The package is designed with interfaces for repositories, enabling:
//
//   - Easy mocking for unit tests (via go:generate mockgen)
//   - Swappable storage implementations
//   - Clear separation of concerns
//
// # Testing
//
// Unit tests typically:
//
//   - Use generated mocks for repositories and database transactions
//   - Validate business logic independently of infrastructure
//   - Cover edge cases such as token expiration, invalid credentials,
//     and transaction failures
//
// # Security Considerations
//
//   - Passwords are never stored in plain text
//   - Refresh tokens are stored in HTTP-only, Secure cookies
//   - SameSite=Strict is used to mitigate CSRF attacks
//   - Token signing uses a configurable secret key
//
// Note: In non-HTTPS environments, Secure cookies may not be transmitted.
// This should be configurable depending on deployment environment.
package services
