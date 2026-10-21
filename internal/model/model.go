// Package model defines API resources and write payloads.
package model

type UserInput struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type User struct {
	ID int64 `json:"id"`
	UserInput
	CreatedAt string `json:"created_at"`
}

type BlogInput struct {
	Title    string  `json:"title"`
	Body     string  `json:"body"`
	AuthorID int64   `json:"author_id"`
	TagIDs   []int64 `json:"tag_ids"`
}

type Blog struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	AuthorID  int64  `json:"author_id"`
	Tags      []Tag  `json:"tags"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type CommentInput struct {
	BlogID   int64  `json:"blog_id"`
	AuthorID int64  `json:"author_id"`
	Body     string `json:"body"`
}

type Comment struct {
	ID int64 `json:"id"`
	CommentInput
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type TagInput struct {
	Name string `json:"name"`
}

type Tag struct {
	ID int64 `json:"id"`
	TagInput
}

func (u User) ResourceID() int64    { return u.ID }
func (b Blog) ResourceID() int64    { return b.ID }
func (c Comment) ResourceID() int64 { return c.ID }
func (t Tag) ResourceID() int64     { return t.ID }
