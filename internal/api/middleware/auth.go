package middleware

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/orrn/spool/internal/db"
	"github.com/orrn/spool/internal/utils"
	"golang.org/x/crypto/bcrypt"
)

const (
	cookieName         = "spool_auth"
	tokenDuration      = 24 * time.Hour
	settingsKeyPassword = "admin_password"
	settingsKeyJWTSecret = "jwt_secret"
)

type Claims struct {
	jwt.RegisteredClaims
	Authenticated bool `json:"authenticated"`
}

type AuthMiddleware struct {
	db     *sql.DB
	secret []byte
}

type LoginRequest struct {
	Password string `json:"password" binding:"required"`
}

type LoginResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required,min=6"`
}

type SetupRequest struct {
	Password string `json:"password" binding:"required,min=6"`
}

type StatusResponse struct {
	Authenticated bool `json:"authenticated"`
	SetupRequired bool `json:"setup_required"`
}

func NewAuthMiddleware(database *sql.DB) (*AuthMiddleware, error) {
	a := &AuthMiddleware{db: database}

	secret, err := a.getOrCreateSecret()
	if err != nil {
		return nil, err
	}
	a.secret = secret

	return a, nil
}

func (a *AuthMiddleware) getOrCreateSecret() ([]byte, error) {
	ctx := context.Background()
	setting, err := db.Settings.GetSetting(ctx, settingsKeyJWTSecret)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			secret := utils.GenerateRandomKey()
			secretHex := hex.EncodeToString(secret)
			if err := db.Settings.SetSetting(ctx, settingsKeyJWTSecret, secretHex, false); err != nil {
				return nil, err
			}
			return secret, nil
		}
		return nil, err
	}
	return hex.DecodeString(setting.Value)
}

func (a *AuthMiddleware) isSetupRequired() bool {
	ctx := context.Background()
	_, err := db.Settings.GetSetting(ctx, settingsKeyPassword)
	return errors.Is(err, sql.ErrNoRows)
}

func (a *AuthMiddleware) generateToken() (string, error) {
	now := time.Now()
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tokenDuration)),
			Issuer:    "spool",
		},
		Authenticated: true,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(a.secret)
}

func (a *AuthMiddleware) validateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return a.secret, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, errors.New("invalid token")
}

func (a *AuthMiddleware) getTokenFromRequest(c *gin.Context) string {
	if cookie, err := c.Cookie(cookieName); err == nil && cookie != "" {
		return cookie
	}

	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		if strings.HasPrefix(authHeader, "Bearer ") {
			return strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	return ""
}

func (a *AuthMiddleware) setAuthCookie(c *gin.Context, token string) {
	c.SetCookie(cookieName, token, int(tokenDuration.Seconds()), "/", "", true, true)
}

func (a *AuthMiddleware) clearAuthCookie(c *gin.Context) {
	c.SetCookie(cookieName, "", -1, "/", "", true, true)
}

func (a *AuthMiddleware) LoginHandler(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, LoginResponse{Success: false, Message: "Invalid request"})
		return
	}

	if a.isSetupRequired() {
		c.JSON(http.StatusForbidden, LoginResponse{Success: false, Message: "Setup required"})
		return
	}

	ctx := context.Background()
	setting, err := db.Settings.GetSetting(ctx, settingsKeyPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, LoginResponse{Success: false, Message: "Server error"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(setting.Value), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, LoginResponse{Success: false, Message: "Invalid password"})
		return
	}

	token, err := a.generateToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, LoginResponse{Success: false, Message: "Failed to generate token"})
		return
	}

	a.setAuthCookie(c, token)
	c.JSON(http.StatusOK, LoginResponse{Success: true})
}

func (a *AuthMiddleware) LogoutHandler(c *gin.Context) {
	a.clearAuthCookie(c)
	c.JSON(http.StatusOK, LoginResponse{Success: true, Message: "Logged out"})
}

func (a *AuthMiddleware) StatusHandler(c *gin.Context) {
	token := a.getTokenFromRequest(c)
	if token == "" {
		c.JSON(http.StatusOK, StatusResponse{Authenticated: false, SetupRequired: a.isSetupRequired()})
		return
	}

	claims, err := a.validateToken(token)
	if err != nil {
		c.JSON(http.StatusOK, StatusResponse{Authenticated: false, SetupRequired: a.isSetupRequired()})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{Authenticated: claims.Authenticated, SetupRequired: false})
}

func (a *AuthMiddleware) ChangePasswordHandler(c *gin.Context) {
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	ctx := context.Background()
	setting, err := db.Settings.GetSetting(ctx, settingsKeyPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Server error"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(setting.Value), []byte(req.CurrentPassword)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Current password is incorrect"})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	if err := db.Settings.SetSetting(ctx, settingsKeyPassword, string(hashedPassword), false); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password"})
		return
	}

	token, err := a.generateToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	a.setAuthCookie(c, token)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Password changed"})
}

func (a *AuthMiddleware) SetupHandler(c *gin.Context) {
	if !a.isSetupRequired() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Setup already completed"})
		return
	}

	var req SetupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request, password must be at least 6 characters"})
		return
	}

	ctx := context.Background()

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	if err := db.Settings.SetSetting(ctx, settingsKeyPassword, string(hashedPassword), false); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save password"})
		return
	}

	token, err := a.generateToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	a.setAuthCookie(c, token)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Setup completed"})
}

func (a *AuthMiddleware) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := a.getTokenFromRequest(c)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			return
		}

		claims, err := a.validateToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			return
		}

		if !claims.Authenticated {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Not authenticated"})
			return
		}

		c.Set("authenticated", true)
		c.Set("claims", claims)
		c.Next()
	}
}

func (a *AuthMiddleware) OptionalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := a.getTokenFromRequest(c)
		if token == "" {
			c.Set("authenticated", false)
			c.Next()
			return
		}

		claims, err := a.validateToken(token)
		if err != nil {
			c.Set("authenticated", false)
			c.Next()
			return
		}

		c.Set("authenticated", claims.Authenticated)
		c.Set("claims", claims)
		c.Next()
	}
}

func GetEncryptionKey(database *sql.DB) ([]byte, error) {
	ctx := context.Background()
	setting, err := db.Settings.GetSetting(ctx, settingsKeyJWTSecret)
	if err != nil {
		return nil, err
	}
	return hex.DecodeString(setting.Value)
}

func EncryptSetting(plaintext string, database *sql.DB) (string, error) {
	key, err := GetEncryptionKey(database)
	if err != nil {
		return "", err
	}
	return utils.Encrypt(plaintext, key)
}

func DecryptSetting(ciphertext string, database *sql.DB) (string, error) {
	key, err := GetEncryptionKey(database)
	if err != nil {
		return "", err
	}
	return utils.Decrypt(ciphertext, key)
}
