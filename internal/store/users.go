package store

import (
	"context"

	"go-api/internal/model"
)

func (s *Store) ListUsers(ctx context.Context, opts ListOptions) ([]model.User, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, email, created_at FROM users ORDER BY id LIMIT ? OFFSET ?", opts.Limit, opts.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []model.User{}
	for rows.Next() {
		var user model.User
		if err := rows.Scan(&user.ID, &user.Name, &user.Email, &user.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) GetUser(ctx context.Context, id int64) (model.User, error) {
	var user model.User
	err := s.db.QueryRowContext(ctx, "SELECT id, name, email, created_at FROM users WHERE id = ?", id).
		Scan(&user.ID, &user.Name, &user.Email, &user.CreatedAt)
	return user, databaseError(err)
}

func (s *Store) CreateUser(ctx context.Context, in model.UserInput) (model.User, error) {
	var user model.User
	err := s.db.QueryRowContext(ctx, "INSERT INTO users (name, email) VALUES (?, ?) RETURNING id, name, email, created_at", in.Name, in.Email).
		Scan(&user.ID, &user.Name, &user.Email, &user.CreatedAt)
	return user, databaseError(err)
}

func (s *Store) UpdateUser(ctx context.Context, id int64, in model.UserInput) (model.User, error) {
	var user model.User
	err := s.db.QueryRowContext(ctx, "UPDATE users SET name = ?, email = ? WHERE id = ? RETURNING id, name, email, created_at", in.Name, in.Email, id).
		Scan(&user.ID, &user.Name, &user.Email, &user.CreatedAt)
	return user, databaseError(err)
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	return changed(s.db.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id))
}
