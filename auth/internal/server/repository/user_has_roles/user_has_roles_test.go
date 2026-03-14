package user_has_roles

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type mockRow struct {
	scan func(dest ...any) error
}

func (m mockRow) Scan(dest ...any) error {
	return m.scan(dest...)
}

type mockRows struct {
	index int
	data  []UserHasRole
	err   error
}

func (m *mockRows) Next() bool {
	return m.index < len(m.data)
}

func (m *mockRows) Scan(dest ...any) error {
	r := m.data[m.index]
	m.index++

	*(dest[0].(*int64)) = r.ID
	*(dest[1].(*pgtype.Text)) = r.UserName
	*(dest[2].(*pgtype.Text)) = r.Role
	*(dest[3].(*pgtype.Timestamp)) = r.CreateTime
	*(dest[4].(*pgtype.Timestamp)) = r.UpdateTime
	*(dest[5].(*pgtype.Bool)) = r.Enabled
	*(dest[6].(*bool)) = r.LocalChange
	*(dest[7].(*pgtype.Bool)) = r.Visible
	*(dest[8].(*int32)) = r.Flags

	return nil
}

func (m *mockRows) Close()                                       {}
func (m *mockRows) Err() error                                   { return m.err }
func (m *mockRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (m *mockRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (m *mockRows) Values() ([]any, error)                       { return nil, nil }
func (m *mockRows) RawValues() [][]byte                          { return nil }
func (m *mockRows) Conn() *pgx.Conn                              { return nil }

type mockDB struct {
	exec  func(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
	query func(context.Context, string, ...interface{}) (pgx.Rows, error)
	row   func(context.Context, string, ...interface{}) pgx.Row
}

func (m mockDB) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	return m.exec(ctx, sql, args...)
}

func (m mockDB) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	return m.query(ctx, sql, args...)
}

func (m mockDB) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	return m.row(ctx, sql, args...)
}

func TestCreateUserHasRoles(t *testing.T) {
	db := mockDB{
		row: func(ctx context.Context, sql string, args ...interface{}) pgx.Row {
			return mockRow{
				scan: func(dest ...any) error {
					*(dest[0].(*int64)) = 1
					return nil
				},
			}
		},
	}

	q := New(db)

	res, err := q.CreateUserHasRoles(context.Background(), CreateUserHasRolesParams{})

	if err != nil {
		t.Fatal(err)
	}

	if res.ID != 1 {
		t.Fatalf("expected ID 1 got %d", res.ID)
	}
}

func TestDeleteUserHasRolesBy(t *testing.T) {
	called := false

	db := mockDB{
		exec: func(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
			called = true
			return pgconn.CommandTag{}, nil
		},
	}

	q := New(db)

	err := q.DeleteUserHasRolesBy(context.Background(), pgtype.Text{})

	if err != nil {
		t.Fatal(err)
	}

	if !called {
		t.Fatal("Exec not called")
	}
}

func TestDeleteUserHasRolesByID_Error(t *testing.T) {
	db := mockDB{
		exec: func(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, errors.New("db error")
		},
	}

	q := New(db)

	err := q.DeleteUserHasRolesByID(context.Background(), 0)

	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListUserHasRoles(t *testing.T) {
	rows := &mockRows{
		data: []UserHasRole{
			{ID: 1},
			{ID: 2},
		},
	}

	db := mockDB{
		query: func(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
			return rows, nil
		},
	}

	q := New(db)

	list, err := q.ListUserHasRoles(context.Background())

	if err != nil {
		t.Fatal(err)
	}

	if len(list) != 2 {
		t.Fatalf("expected 2 rows got %d", len(list))
	}
}

func TestListUserHasRoles_QueryError(t *testing.T) {
	db := mockDB{
		query: func(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
			return nil, errors.New("query error")
		},
	}

	q := New(db)

	_, err := q.ListUserHasRoles(context.Background())

	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListUserHasRolesByRole(t *testing.T) {
	rows := &mockRows{
		data: []UserHasRole{{ID: 1}},
	}

	db := mockDB{
		query: func(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
			return rows, nil
		},
	}

	q := New(db)

	res, err := q.ListUserHasRolesByRole(context.Background(), pgtype.Text{})

	if err != nil {
		t.Fatal(err)
	}

	if len(res) != 1 {
		t.Fatal("expected one row")
	}
}

func TestListUserHasRolesByUserName(t *testing.T) {
	rows := &mockRows{
		data: []UserHasRole{{ID: 5}},
	}

	db := mockDB{
		query: func(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
			return rows, nil
		},
	}

	q := New(db)

	res, err := q.ListUserHasRolesByUserName(context.Background(), pgtype.Text{})

	if err != nil {
		t.Fatal(err)
	}

	if res[0].ID != 5 {
		t.Fatal("unexpected result")
	}
}

func TestUpdateUserHasRoles(t *testing.T) {
	called := false

	db := mockDB{
		exec: func(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
			called = true
			return pgconn.CommandTag{}, nil
		},
	}

	q := New(db)

	err := q.UpdateUserHasRoles(context.Background(), UpdateUserHasRolesParams{})

	if err != nil {
		t.Fatal(err)
	}

	if !called {
		t.Fatal("Exec not called")
	}
}
