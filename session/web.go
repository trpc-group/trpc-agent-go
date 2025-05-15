package session

import (
	"context"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

// contextKey is a type for context keys specific to the session package.
type contextKey string

// Context keys.
const (
	sessionKey contextKey = "tRPC_ADK_Session"
	managerKey contextKey = "tRPC_ADK_SessionManager"
)

// WebOptions configures the session web middleware.
type WebOptions struct {
	// CookieName is the name of the cookie used to store the session ID.
	CookieName string

	// CookiePath is the path of the cookie.
	CookiePath string

	// CookieDomain is the domain of the cookie.
	CookieDomain string

	// CookieMaxAge is the maximum age of the cookie in seconds.
	CookieMaxAge int

	// CookieSecure indicates whether the cookie should only be sent over HTTPS.
	CookieSecure bool

	// CookieHTTPOnly indicates whether the cookie should only be accessible via HTTP.
	CookieHTTPOnly bool

	// HeaderName is the name of the header used to store the session ID.
	HeaderName string

	// UseHeaders indicates whether to use headers instead of cookies.
	UseHeaders bool
}

// DefaultWebOptions returns default web options.
func DefaultWebOptions() WebOptions {
	return WebOptions{
		CookieName:     "tRPC_Session_ID",
		CookiePath:     "/",
		CookieMaxAge:   86400 * 30, // 30 days
		CookieHTTPOnly: true,
		HeaderName:     "X-Session-ID",
	}
}

// Middleware returns a middleware function that adds session management to HTTP handlers.
func Middleware(manager Manager, options ...WebOptions) func(http.Handler) http.Handler {
	// Use default options if none provided
	opts := DefaultWebOptions()
	if len(options) > 0 {
		opts = options[0]
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract session ID from cookie or header
			var sessionID string
			if opts.UseHeaders {
				sessionID = r.Header.Get(opts.HeaderName)
			} else {
				cookie, err := r.Cookie(opts.CookieName)
				if err == nil {
					sessionID = cookie.Value
				}
			}

			// Get or create session
			session, err := manager.Get(r.Context(), sessionID)
			if err != nil {
				// Handle error (e.g., log it), but continue with a new session
				session, _ = manager.Create(r.Context())
			}

			// Set session ID in cookie or header for new sessions
			if sessionID == "" || sessionID != session.ID() {
				if opts.UseHeaders {
					// Set header for response
					w.Header().Set(opts.HeaderName, session.ID())
				} else {
					// Set cookie
					http.SetCookie(w, &http.Cookie{
						Name:     opts.CookieName,
						Value:    session.ID(),
						Path:     opts.CookiePath,
						Domain:   opts.CookieDomain,
						MaxAge:   opts.CookieMaxAge,
						Secure:   opts.CookieSecure,
						HttpOnly: opts.CookieHTTPOnly,
						SameSite: http.SameSiteLaxMode,
					})
				}
			}

			// Store session and manager in context
			ctx := context.WithValue(r.Context(), sessionKey, session)
			ctx = context.WithValue(ctx, managerKey, manager)

			// Call the next handler with the updated context
			next.ServeHTTP(w, r.WithContext(ctx))

			// Save session after handler completes (if it was modified)
			if store, ok := manager.(StoreProvider); ok {
				// Get metadata to see if the session was updated
				if _, ok := session.GetMetadata("_modified"); ok || session.LastUpdated().After(time.Now().Add(-time.Minute)) {
					// Update the session in the store
					err := store.Save(ctx, session)
					if err != nil {
						// Log the error, but don't affect the response
						// Since the response is already being sent
					}
				}
			}
		})
	}
}

// GetSession retrieves the session from the context.
func GetSession(ctx context.Context) (memory.Session, bool) {
	session, ok := ctx.Value(sessionKey).(memory.Session)
	return session, ok
}

// GetManager retrieves the session manager from the context.
func GetManager(ctx context.Context) (Manager, bool) {
	manager, ok := ctx.Value(managerKey).(Manager)
	return manager, ok
}

// MustGetSession retrieves the session from the context, panicking if not found.
func MustGetSession(ctx context.Context) memory.Session {
	session, ok := GetSession(ctx)
	if !ok {
		panic("session not found in context")
	}
	return session
}

// MustGetManager retrieves the session manager from the context, panicking if not found.
func MustGetManager(ctx context.Context) Manager {
	manager, ok := GetManager(ctx)
	if !ok {
		panic("session manager not found in context")
	}
	return manager
}
