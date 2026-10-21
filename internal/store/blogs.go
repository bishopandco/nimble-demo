package store

import (
	"context"
	"database/sql"

	"go-api/internal/model"
)

const blogColumns = "id, title, body, author_id, created_at, updated_at"

func (s *Store) ListBlogs(ctx context.Context, opts ListOptions) ([]model.Blog, error) {
	query := "SELECT " + blogColumns + " FROM blogs"
	args := []any{}
	if opts.RelatedID != 0 {
		query += " WHERE id IN (SELECT blog_id FROM blog_tags WHERE tag_id = ?)"
		args = append(args, opts.RelatedID)
	}
	query += " ORDER BY id LIMIT ? OFFSET ?"
	args = append(args, opts.Limit, opts.Offset)
	// Read blogs and their tags in one snapshot, so concurrent writes cannot mix versions.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	blogs := []model.Blog{}
	for rows.Next() {
		blog, err := scanBlog(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		blogs = append(blogs, blog)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range blogs {
		if err := loadTags(ctx, tx, &blogs[i]); err != nil {
			return nil, err
		}
	}
	return blogs, tx.Commit()
}

func (s *Store) GetBlog(ctx context.Context, id int64) (model.Blog, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return model.Blog{}, err
	}
	defer tx.Rollback()
	blog, err := getBlog(ctx, tx, id)
	if err != nil {
		return model.Blog{}, err
	}
	return blog, tx.Commit()
}

func (s *Store) CreateBlog(ctx context.Context, in model.BlogInput) (model.Blog, error) {
	return s.writeBlog(ctx, 0, in)
}

func (s *Store) UpdateBlog(ctx context.Context, id int64, in model.BlogInput) (model.Blog, error) {
	return s.writeBlog(ctx, id, in)
}

func (s *Store) writeBlog(ctx context.Context, id int64, in model.BlogInput) (model.Blog, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Blog{}, err
	}
	defer tx.Rollback()
	if id == 0 {
		err = tx.QueryRowContext(ctx, "INSERT INTO blogs (title, body, author_id) VALUES (?, ?, ?) RETURNING id", in.Title, in.Body, in.AuthorID).Scan(&id)
	} else {
		err = changed(tx.ExecContext(ctx, "UPDATE blogs SET title = ?, body = ?, author_id = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?", in.Title, in.Body, in.AuthorID, id))
	}
	if err != nil {
		return model.Blog{}, databaseError(err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM blog_tags WHERE blog_id = ?", id); err != nil {
		return model.Blog{}, err
	}
	for _, tagID := range in.TagIDs {
		if _, err := tx.ExecContext(ctx, "INSERT INTO blog_tags (blog_id, tag_id) VALUES (?, ?)", id, tagID); err != nil {
			return model.Blog{}, databaseError(err)
		}
	}
	blog, err := getBlog(ctx, tx, id)
	if err != nil {
		return model.Blog{}, err
	}
	return blog, databaseError(tx.Commit())
}

func (s *Store) DeleteBlog(ctx context.Context, id int64) error {
	return changed(s.db.ExecContext(ctx, "DELETE FROM blogs WHERE id = ?", id))
}

func getBlog(ctx context.Context, tx *sql.Tx, id int64) (model.Blog, error) {
	blog, err := scanBlog(tx.QueryRowContext(ctx, "SELECT "+blogColumns+" FROM blogs WHERE id = ?", id))
	if err == nil {
		err = loadTags(ctx, tx, &blog)
	}
	return blog, err
}

func scanBlog(row scanner) (model.Blog, error) {
	var blog model.Blog
	err := row.Scan(&blog.ID, &blog.Title, &blog.Body, &blog.AuthorID, &blog.CreatedAt, &blog.UpdatedAt)
	return blog, databaseError(err)
}

func loadTags(ctx context.Context, tx *sql.Tx, blog *model.Blog) error {
	rows, err := tx.QueryContext(ctx, "SELECT tags.id, tags.name FROM tags JOIN blog_tags ON tags.id = blog_tags.tag_id WHERE blog_tags.blog_id = ? ORDER BY tags.id", blog.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	blog.Tags = []model.Tag{}
	for rows.Next() {
		var tag model.Tag
		if err := rows.Scan(&tag.ID, &tag.Name); err != nil {
			return err
		}
		blog.Tags = append(blog.Tags, tag)
	}
	return rows.Err()
}
