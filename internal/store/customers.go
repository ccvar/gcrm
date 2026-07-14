package store

import (
	"database/sql"
	"errors"
)

type Customer struct {
	ID           int64
	Name         string
	Company      string
	Phone        string
	Wechat       string
	Email        string
	Source       string
	Stage        string // lead / contacted / negotiating / won / lost
	Intent       string // unknown / low / medium / high（AI 判定，可人工纠正）
	IntentReason string
	Notes        string
	CreatedAt    int64
	UpdatedAt    int64
}

const customerCols = `id, name, company, phone, wechat, email, source, stage, intent, intent_reason, notes, created_at, updated_at`

func scanCustomer(row interface{ Scan(...any) error }) (*Customer, error) {
	c := &Customer{}
	err := row.Scan(&c.ID, &c.Name, &c.Company, &c.Phone, &c.Wechat, &c.Email, &c.Source,
		&c.Stage, &c.Intent, &c.IntentReason, &c.Notes, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Store) CreateCustomer(c *Customer) (int64, error) {
	t := now()
	res, err := s.db.Exec(`INSERT INTO customers(name, company, phone, wechat, email, source, stage, intent, intent_reason, notes, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.Name, c.Company, c.Phone, c.Wechat, c.Email, c.Source,
		defaultStr(c.Stage, "lead"), defaultStr(c.Intent, "unknown"), c.IntentReason, c.Notes, t, t)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func defaultStr(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// Customers 按更新时间倒序列出；q 非空时对姓名/公司/电话/微信做模糊匹配。
func (s *Store) Customers(q string, limit int) ([]Customer, error) {
	if limit <= 0 {
		limit = 200
	}
	var rows *sql.Rows
	var err error
	if q != "" {
		like := "%" + q + "%"
		rows, err = s.db.Query(`SELECT `+customerCols+` FROM customers
			WHERE name LIKE ? OR company LIKE ? OR phone LIKE ? OR wechat LIKE ?
			ORDER BY updated_at DESC LIMIT ?`, like, like, like, like, limit)
	} else {
		rows, err = s.db.Query(`SELECT `+customerCols+` FROM customers ORDER BY updated_at DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Customer
	for rows.Next() {
		c, err := scanCustomer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) Customer(id int64) (*Customer, error) {
	c, err := scanCustomer(s.db.QueryRow(`SELECT `+customerCols+` FROM customers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (s *Store) UpdateCustomerProfile(id int64, name, company, phone, wechat, email, source, notes string) error {
	_, err := s.db.Exec(`UPDATE customers SET name=?, company=?, phone=?, wechat=?, email=?, source=?, notes=?, updated_at=? WHERE id=?`,
		name, company, phone, wechat, email, source, notes, now(), id)
	return err
}

func (s *Store) UpdateCustomerStage(id int64, stage string) error {
	_, err := s.db.Exec(`UPDATE customers SET stage=?, updated_at=? WHERE id=?`, stage, now(), id)
	return err
}

func (s *Store) UpdateCustomerIntent(id int64, intent, reason string) error {
	_, err := s.db.Exec(`UPDATE customers SET intent=?, intent_reason=?, updated_at=? WHERE id=?`, intent, reason, now(), id)
	return err
}

// ---------- 交互记录 ----------

type Interaction struct {
	ID         int64
	CustomerID int64
	Channel    string // wechat / phone / email / meeting / other
	Direction  string // in（客户→我） / out（我→客户）
	Content    string
	OccurredAt int64
	AISummary  string
	AIJSON     string
	CreatedAt  int64
}

func (s *Store) AddInteraction(it *Interaction) (int64, error) {
	if it.OccurredAt == 0 {
		it.OccurredAt = now()
	}
	res, err := s.db.Exec(`INSERT INTO interactions(customer_id, channel, direction, content, occurred_at, ai_summary, ai_json, created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		it.CustomerID, defaultStr(it.Channel, "other"), defaultStr(it.Direction, "out"),
		it.Content, it.OccurredAt, it.AISummary, it.AIJSON, now())
	if err != nil {
		return 0, err
	}
	// 有新交互就把客户顶到列表前面
	_, _ = s.db.Exec(`UPDATE customers SET updated_at=? WHERE id=?`, now(), it.CustomerID)
	return res.LastInsertId()
}

func (s *Store) Interaction(id int64) (*Interaction, error) {
	it := &Interaction{}
	err := s.db.QueryRow(`SELECT id, customer_id, channel, direction, content, occurred_at, ai_summary, ai_json, created_at
		FROM interactions WHERE id = ?`, id).
		Scan(&it.ID, &it.CustomerID, &it.Channel, &it.Direction, &it.Content, &it.OccurredAt, &it.AISummary, &it.AIJSON, &it.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return it, err
}

// Interactions 按发生时间倒序（最新在前）。
func (s *Store) Interactions(customerID int64, limit int) ([]Interaction, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, customer_id, channel, direction, content, occurred_at, ai_summary, ai_json, created_at
		FROM interactions WHERE customer_id = ? ORDER BY occurred_at DESC, id DESC LIMIT ?`, customerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Interaction
	for rows.Next() {
		var it Interaction
		if err := rows.Scan(&it.ID, &it.CustomerID, &it.Channel, &it.Direction, &it.Content,
			&it.OccurredAt, &it.AISummary, &it.AIJSON, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) SetInteractionAI(id int64, summary, aiJSON string) error {
	_, err := s.db.Exec(`UPDATE interactions SET ai_summary=?, ai_json=? WHERE id=?`, summary, aiJSON, id)
	return err
}

// ---------- 任务 ----------

type Task struct {
	ID         int64
	CustomerID int64
	DealID     int64
	Title      string
	Detail     string
	AIDraft    string // AI 起草的跟进话术，人来编辑发送
	Source     string // manual / ai
	Status     string // open / done / canceled
	DueAt      int64
	CreatedAt  int64
	DoneAt     int64
}

// TaskRow 是带客户名的任务行，供行动队列展示。
type TaskRow struct {
	Task
	CustomerName string
}

func (s *Store) CreateTask(t *Task) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO tasks(customer_id, deal_id, title, detail, ai_draft, source, status, due_at, created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		t.CustomerID, t.DealID, t.Title, t.Detail, t.AIDraft,
		defaultStr(t.Source, "manual"), "open", t.DueAt, now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Task(id int64) (*Task, error) {
	t := &Task{}
	err := s.db.QueryRow(`SELECT id, customer_id, deal_id, title, detail, ai_draft, source, status, due_at, created_at, done_at
		FROM tasks WHERE id = ?`, id).
		Scan(&t.ID, &t.CustomerID, &t.DealID, &t.Title, &t.Detail, &t.AIDraft, &t.Source, &t.Status, &t.DueAt, &t.CreatedAt, &t.DoneAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// OpenTaskRows 列出全部待办（未排期的排最后），供首页行动队列分桶。
func (s *Store) OpenTaskRows() ([]TaskRow, error) {
	rows, err := s.db.Query(`SELECT t.id, t.customer_id, t.deal_id, t.title, t.detail, t.ai_draft, t.source, t.status, t.due_at, t.created_at, t.done_at, c.name
		FROM tasks t JOIN customers c ON c.id = t.customer_id
		WHERE t.status = 'open'
		ORDER BY CASE WHEN t.due_at = 0 THEN 1 ELSE 0 END, t.due_at ASC, t.id DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskRow
	for rows.Next() {
		var t TaskRow
		if err := rows.Scan(&t.ID, &t.CustomerID, &t.DealID, &t.Title, &t.Detail, &t.AIDraft,
			&t.Source, &t.Status, &t.DueAt, &t.CreatedAt, &t.DoneAt, &t.CustomerName); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) TasksForCustomer(customerID int64) ([]Task, error) {
	rows, err := s.db.Query(`SELECT id, customer_id, deal_id, title, detail, ai_draft, source, status, due_at, created_at, done_at
		FROM tasks WHERE customer_id = ? AND status = 'open'
		ORDER BY CASE WHEN due_at = 0 THEN 1 ELSE 0 END, due_at ASC, id DESC LIMIT 100`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.CustomerID, &t.DealID, &t.Title, &t.Detail, &t.AIDraft,
			&t.Source, &t.Status, &t.DueAt, &t.CreatedAt, &t.DoneAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) CompleteTask(id int64) error {
	_, err := s.db.Exec(`UPDATE tasks SET status='done', done_at=? WHERE id=? AND status='open'`, now(), id)
	return err
}

// ---------- 商机 ----------

type Deal struct {
	ID            int64
	CustomerID    int64
	Title         string
	AmountCents   int64
	Stage         string // open / won / lost
	ExpectedClose int64
	ClosedAt      int64
	LostReason    string
	Review        string // AI 复盘正文（关单后生成）
	ReviewLessons string // 可复用的经验条目，换行分隔
	CreatedAt     int64
	UpdatedAt     int64
}

const dealCols = `id, customer_id, title, amount_cents, stage, expected_close, closed_at, lost_reason, review, review_lessons, created_at, updated_at`

func scanDeal(row interface{ Scan(...any) error }) (*Deal, error) {
	d := &Deal{}
	err := row.Scan(&d.ID, &d.CustomerID, &d.Title, &d.AmountCents, &d.Stage, &d.ExpectedClose,
		&d.ClosedAt, &d.LostReason, &d.Review, &d.ReviewLessons, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Store) CreateDeal(d *Deal) (int64, error) {
	t := now()
	res, err := s.db.Exec(`INSERT INTO deals(customer_id, title, amount_cents, stage, expected_close, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?)`,
		d.CustomerID, d.Title, d.AmountCents, defaultStr(d.Stage, "open"), d.ExpectedClose, t, t)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Deal(id int64) (*Deal, error) {
	d, err := scanDeal(s.db.QueryRow(`SELECT `+dealCols+` FROM deals WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

func (s *Store) DealsForCustomer(customerID int64) ([]Deal, error) {
	rows, err := s.db.Query(`SELECT `+dealCols+` FROM deals WHERE customer_id = ? ORDER BY id DESC LIMIT 50`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Deal
	for rows.Next() {
		d, err := scanDeal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Store) CloseDeal(id int64, stage, lostReason string) error {
	_, err := s.db.Exec(`UPDATE deals SET stage=?, lost_reason=?, closed_at=?, updated_at=? WHERE id=? AND stage='open'`,
		stage, lostReason, now(), now(), id)
	return err
}

func (s *Store) SetDealReview(id int64, review, lessons string) error {
	_, err := s.db.Exec(`UPDATE deals SET review=?, review_lessons=?, updated_at=? WHERE id=?`, review, lessons, now(), id)
	return err
}

// DealRow 是带客户名的商机行，供复盘页与 API 使用。
type DealRow struct {
	Deal
	CustomerName string
}

// ClosedDealRows 按关单时间倒序列出已成交/已丢单的商机。
func (s *Store) ClosedDealRows(limit int) ([]DealRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT d.id, d.customer_id, d.title, d.amount_cents, d.stage, d.expected_close,
		d.closed_at, d.lost_reason, d.review, d.review_lessons, d.created_at, d.updated_at, c.name
		FROM deals d JOIN customers c ON c.id = d.customer_id
		WHERE d.stage IN ('won','lost') ORDER BY d.closed_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DealRow
	for rows.Next() {
		var d DealRow
		if err := rows.Scan(&d.ID, &d.CustomerID, &d.Title, &d.AmountCents, &d.Stage, &d.ExpectedClose,
			&d.ClosedAt, &d.LostReason, &d.Review, &d.ReviewLessons, &d.CreatedAt, &d.UpdatedAt, &d.CustomerName); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ---------- 首页统计 ----------

type Stats struct {
	Customers       int
	HighIntent      int
	OpenDeals       int
	OpenAmountCents int64
	OpenTasks       int
}

func (s *Store) DashboardStats() (Stats, error) {
	var st Stats
	err := s.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM customers),
		(SELECT COUNT(*) FROM customers WHERE intent='high' AND stage NOT IN ('won','lost')),
		(SELECT COUNT(*) FROM deals WHERE stage='open'),
		(SELECT COALESCE(SUM(amount_cents),0) FROM deals WHERE stage='open'),
		(SELECT COUNT(*) FROM tasks WHERE status='open')`).
		Scan(&st.Customers, &st.HighIntent, &st.OpenDeals, &st.OpenAmountCents, &st.OpenTasks)
	return st, err
}
