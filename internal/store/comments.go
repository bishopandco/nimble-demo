package store

import (
	"context"

	"go-api/internal/model"
)

const commentColumns = "id, blog_id, author_id, body, created_at, updated_at"

func (s *Store) ListComments(ctx context.Context, opts ListOptions) ([]model.Comment, error) {
	query := "SELECT " + commentColumns + " FROM comments"
	args := []any{}
	if opts.RelatedID != 0 {
		query += " WHERE blog_id = ?"
		args = append(args, opts.RelatedID)
	}
	query += " ORDER BY id LIMIT ? OFFSET ?"
	args = append(args, opts.Limit, opts.Offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	comments := []model.Comment{}
	for rows.Next() {
		comment, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	return comments, rows.Err()
}

func (s *Store) GetComment(ctx context.Context, id int64) (model.Comment, error) {
	return scanComment(s.db.QueryRowContext(ctx, "SELECT "+commentColumns+" FROM comments WHERE id = ?", id))
}

func (s *Store) CreateComment(ctx context.Context, in model.CommentInput) (model.Comment, error) {
	return scanComment(s.db.QueryRowContext(ctx, "INSERT INTO comments (blog_id, author_id, body) VALUES (?, ?, ?) RETURNING "+commentColumns, in.BlogID, in.AuthorID, in.Body))
}

func (s *Store) UpdateComment(ctx context.Context, id int64, in model.CommentInput) (model.Comment, error) {
	return scanComment(s.db.QueryRowContext(ctx, "UPDATE comments SET blog_id = ?, author_id = ?, body = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? RETURNING "+commentColumns, in.BlogID, in.AuthorID, in.Body, id))
}

func (s *Store) DeleteComment(ctx context.Context, id int64) error {
	return changed(s.db.ExecContext(ctx, "DELETE FROM comments WHERE id = ?", id))
}

type scanner interface{ Scan(...any) error }

func scanComment(row scanner) (model.Comment, error) {
	var comment model.Comment
	err := row.Scan(&comment.ID, &comment.BlogID, &comment.AuthorID, &comment.Body, &comment.CreatedAt, &comment.UpdatedAt)
	return comment, databaseError(err)
}
