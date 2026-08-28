package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/lerenn/seedly/internal/db"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrForbidden          = errors.New("forbidden")
)

type Service struct {
	db         *db.DB
	sessionTTL time.Duration
}

func New(database *db.DB, sessionTTL time.Duration) *Service {
	return &Service{db: database, sessionTTL: sessionTTL}
}

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (s *Service) BootstrapAdmin(ctx context.Context, username, password string) error {
	n, err := s.db.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.db.CreateUser(ctx, username, hash, db.RoleAdmin)
	return err
}

func (s *Service) Login(ctx context.Context, username, password string) (token string, user *db.User, err error) {
	user, err = s.db.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, ErrInvalidCredentials
		}
		return "", nil, err
	}
	if !CheckPassword(user.PasswordHash, password) {
		return "", nil, ErrInvalidCredentials
	}
	token, err = randomToken(32)
	if err != nil {
		return "", nil, err
	}
	sessionID, err := randomToken(16)
	if err != nil {
		return "", nil, err
	}
	if err := s.db.CreateSession(ctx, db.Session{
		ID:        sessionID,
		UserID:    user.ID,
		TokenHash: hashToken(token),
		ExpiresAt: time.Now().UTC().Add(s.sessionTTL),
	}); err != nil {
		return "", nil, err
	}
	_ = s.db.DeleteExpiredSessions(ctx)
	return token, user, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	sess, err := s.db.GetSessionByTokenHash(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	return s.db.DeleteSession(ctx, sess.ID)
}

func (s *Service) UserFromToken(ctx context.Context, token string) (*db.User, error) {
	if token == "" {
		return nil, ErrUnauthorized
	}
	sess, err := s.db.GetSessionByTokenHash(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUnauthorized
		}
		return nil, err
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		_ = s.db.DeleteSession(ctx, sess.ID)
		return nil, ErrUnauthorized
	}
	user, err := s.db.GetUserByID(ctx, sess.UserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUnauthorized
		}
		return nil, err
	}
	return user, nil
}

func (s *Service) CreateUser(ctx context.Context, actor *db.User, username, password string, role db.Role) (*db.User, error) {
	if actor.Role != db.RoleAdmin {
		return nil, ErrForbidden
	}
	if role != db.RoleAdmin && role != db.RoleUser {
		return nil, fmt.Errorf("invalid role")
	}
	if username == "" || password == "" {
		return nil, fmt.Errorf("username and password required")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	return s.db.CreateUser(ctx, username, hash, role)
}

func (s *Service) RenameUser(ctx context.Context, actor *db.User, id int64, username string) (*db.User, error) {
	if actor.Role != db.RoleAdmin {
		return nil, ErrForbidden
	}
	if username == "" {
		return nil, fmt.Errorf("username required")
	}
	if _, err := s.db.GetUserByID(ctx, id); err != nil {
		return nil, err
	}
	if err := s.db.UpdateUsername(ctx, id, username); err != nil {
		return nil, err
	}
	return s.db.GetUserByID(ctx, id)
}

func (s *Service) DeleteUser(ctx context.Context, actor *db.User, id int64) error {
	if actor.Role != db.RoleAdmin {
		return ErrForbidden
	}
	if actor.ID == id {
		return fmt.Errorf("cannot delete your own account")
	}
	target, err := s.db.GetUserByID(ctx, id)
	if err != nil {
		return err
	}
	if target.Role == db.RoleAdmin {
		n, err := s.db.CountAdmins(ctx)
		if err != nil {
			return err
		}
		if n <= 1 {
			return fmt.Errorf("cannot delete the last admin")
		}
	}
	_ = s.db.DeleteSessionsByUserID(ctx, id)
	return s.db.DeleteUser(ctx, id)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
