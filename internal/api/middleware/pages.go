package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/orrn/spool/internal/db"
)

const minPasswordLen = 6

// SetupRequired reports whether no admin password exists yet.
func (a *AuthMiddleware) SetupRequired() bool {
	return a.isSetupRequired()
}

// wantsHTML is true for a browser navigation (not an HTMX or API call).
func wantsHTML(c *gin.Context) bool {
	return c.Request.Method == http.MethodGet &&
		c.GetHeader("HX-Request") != "true" &&
		strings.Contains(c.GetHeader("Accept"), "text/html")
}

// RequirePageAuth is RequireAuth for pages a person opens in a browser: with no
// valid session it redirects to /login instead of answering with raw JSON.
// API and HTMX calls keep the 401 JSON answer.
func (a *AuthMiddleware) RequirePageAuth() gin.HandlerFunc {
	inner := a.RequireAuth()
	return func(c *gin.Context) {
		if _, machine := orrnToken(c); !machine && wantsHTML(c) {
			claims, err := a.validateToken(a.getTokenFromRequest(c))
			if err != nil || !claims.Authenticated {
				c.Redirect(http.StatusSeeOther, "/login")
				c.Abort()
				return
			}
		}
		inner(c)
	}
}

// LoginPage renders the sign-in form, or the first-time password form.
func (a *AuthMiddleware) LoginPage(c *gin.Context) {
	c.HTML(http.StatusOK, "login.html", gin.H{"FirstTimeSetup": a.isSetupRequired()})
}

// LoginForm handles the login page's plain form POST: it signs in (or sets the
// first password), stores the session cookie and goes to the dashboard.
func (a *AuthMiddleware) LoginForm(c *gin.Context) {
	fail := func(setup bool, status int, msg string) {
		c.HTML(status, "login.html", gin.H{"FirstTimeSetup": setup, "Error": msg})
	}
	password := c.PostForm("password")
	ctx := context.Background()

	if a.isSetupRequired() {
		if len(password) < minPasswordLen {
			fail(true, http.StatusBadRequest, "Password must be at least 6 characters")
			return
		}
		if password != c.PostForm("confirm_password") {
			fail(true, http.StatusBadRequest, "Passwords do not match")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			fail(true, http.StatusInternalServerError, "Failed to hash password")
			return
		}
		if err := db.Settings.SetSetting(ctx, settingsKeyPassword, string(hash), false); err != nil {
			fail(true, http.StatusInternalServerError, "Failed to save password")
			return
		}
	} else {
		setting, err := db.Settings.GetSetting(ctx, settingsKeyPassword)
		if err != nil {
			fail(false, http.StatusInternalServerError, "Server error")
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(setting.Value), []byte(password)) != nil {
			fail(false, http.StatusUnauthorized, "Invalid password")
			return
		}
	}

	token, err := a.generateToken()
	if err != nil {
		fail(false, http.StatusInternalServerError, "Failed to start session")
		return
	}
	a.setAuthCookie(c, token)
	c.Redirect(http.StatusSeeOther, "/")
}

// LogoutRedirect ends the session and returns to the login page.
func (a *AuthMiddleware) LogoutRedirect(c *gin.Context) {
	a.clearAuthCookie(c)
	c.Redirect(http.StatusSeeOther, "/login")
}
