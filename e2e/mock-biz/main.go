// Package main is a Gin mock third-party business system for ActWeave E2E.
// Domain: corporate expense claims + manager approval + department budget.
// Auth: Bearer access tokens (REQUEST_PASSTHROUGH target).
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

const defaultAddr = ":18080"

type role string

const (
	roleEmployee role = "employee"
	roleManager  role = "manager"
)

type user struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Department  string `json:"department"`
	Role        role   `json:"role"`
	// ManagerUsername is who approves this user's claims (empty for top manager).
	ManagerUsername string `json:"managerUsername,omitempty"`
}

type tokenRecord struct {
	Token     string
	Username  string
	ExpiresAt time.Time
}

type expenseStatus string

const (
	statusDraft     expenseStatus = "DRAFT"
	statusSubmitted expenseStatus = "SUBMITTED"
	statusApproved  expenseStatus = "APPROVED"
	statusRejected  expenseStatus = "REJECTED"
)

type expense struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	Category     string        `json:"category"`
	AmountCNY    float64       `json:"amountCny"`
	Currency     string        `json:"currency"`
	Reason       string        `json:"reason"`
	Status       expenseStatus `json:"status"`
	Submitter    string        `json:"submitterUsername"`
	Approver     string        `json:"approverUsername,omitempty"`
	DecisionNote string        `json:"decisionNote,omitempty"`
	CreatedAt    time.Time     `json:"createdAt"`
	UpdatedAt    time.Time     `json:"updatedAt"`
	SubmittedAt  *time.Time    `json:"submittedAt,omitempty"`
	DecidedAt    *time.Time    `json:"decidedAt,omitempty"`
}

type store struct {
	mu       sync.RWMutex
	users    map[string]user
	tokens   map[string]tokenRecord
	expenses map[string]*expense
	// department budget remaining CNY
	budgets map[string]float64
	seq     atomic.Uint64
}

func newStore() *store {
	s := &store{
		users: map[string]user{
			"wang.li": {
				ID: "u-wang", Username: "wang.li", DisplayName: "王丽",
				Department: "销售一部", Role: roleEmployee, ManagerUsername: "chen.wei",
			},
			"chen.wei": {
				ID: "u-chen", Username: "chen.wei", DisplayName: "陈伟",
				Department: "销售一部", Role: roleManager,
			},
			"zhao.min": {
				ID: "u-zhao", Username: "zhao.min", DisplayName: "赵敏",
				Department: "销售一部", Role: roleEmployee, ManagerUsername: "chen.wei",
			},
		},
		tokens:   map[string]tokenRecord{},
		expenses: map[string]*expense{},
		budgets: map[string]float64{
			"销售一部": 50000,
		},
	}
	// Seed one pending approval for manager multi-tool scenarios.
	now := time.Now().UTC()
	sub := now.Add(-2 * time.Hour)
	s.expenses["exp-seed-001"] = &expense{
		ID: "exp-seed-001", Title: "客户拜访差旅", Category: "差旅",
		AmountCNY: 2800, Currency: "CNY", Reason: "拜访华东重点客户",
		Status: statusSubmitted, Submitter: "zhao.min", Approver: "chen.wei",
		CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: sub, SubmittedAt: &sub,
	}
	s.seq.Store(100)
	return s
}

func (s *store) nextID(prefix string) string {
	n := s.seq.Add(1)
	return fmt.Sprintf("%s-%d", prefix, n)
}

func main() {
	addr := os.Getenv("MOCK_BIZ_ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), requestLog(), cors())

	st := newStore()

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "mock-corp-expense"})
	})
	r.StaticFile("/openapi.yaml", "./openapi.yaml")
	r.StaticFile("/openapi.json", "./openapi.json")

	// Issue business tokens for E2E (simulates IdP / business SSO).
	r.POST("/oauth/token", func(c *gin.Context) {
		var body struct {
			Username string `json:"username" form:"username"`
			// password ignored for mock; accept any non-empty or "demo"
			Password string `json:"password" form:"password"`
		}
		_ = c.ShouldBind(&body)
		if body.Username == "" {
			body.Username = c.PostForm("username")
		}
		if body.Username == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "username required"})
			return
		}
		st.mu.RLock()
		u, ok := st.users[body.Username]
		st.mu.RUnlock()
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unknown user"})
			return
		}
		tok := "biz-" + randomHex(16)
		exp := time.Now().UTC().Add(2 * time.Hour)
		st.mu.Lock()
		st.tokens[tok] = tokenRecord{Token: tok, Username: u.Username, ExpiresAt: exp}
		st.mu.Unlock()
		c.JSON(http.StatusOK, gin.H{
			"access_token": tok,
			"token_type":   "Bearer",
			"expires_in":   int(time.Until(exp).Seconds()),
			"username":     u.Username,
			"displayName":  u.DisplayName,
			"role":         u.Role,
		})
	})

	api := r.Group("/v1")
	api.Use(st.authMiddleware())
	{
		api.GET("/me", st.handleMe)
		api.GET("/expenses", st.handleListExpenses)
		api.POST("/expenses", st.handleCreateExpense)
		api.GET("/expenses/:id", st.handleGetExpense)
		api.POST("/expenses/:id/submit", st.handleSubmitExpense)
		api.GET("/approvals/pending", st.handlePendingApprovals)
		api.POST("/approvals/:id/decide", st.handleDecideApproval)
		api.GET("/budget/summary", st.handleBudgetSummary)
	}

	fmt.Printf("mock-corp-expense listening on %s\n", addr)
	if err := r.Run(addr); err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}
}

func cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func requestLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		auth := c.GetHeader("Authorization")
		hint := ""
		if strings.HasPrefix(auth, "Bearer ") {
			t := strings.TrimPrefix(auth, "Bearer ")
			if len(t) > 12 {
				hint = t[:8] + "…"
			} else if t != "" {
				hint = "***"
			}
		}
		fmt.Printf("%s %s -> %d (%s) auth=%s\n",
			c.Request.Method, c.Request.URL.Path, c.Writer.Status(),
			time.Since(start).Truncate(time.Millisecond), hint)
	}
}

func (s *store) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing_bearer", "message": "Authorization: Bearer <token> required",
			})
			return
		}
		tok := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		s.mu.RLock()
		rec, ok := s.tokens[tok]
		s.mu.RUnlock()
		if !ok || time.Now().After(rec.ExpiresAt) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid_token", "message": "token invalid or expired",
			})
			return
		}
		s.mu.RLock()
		u, ok := s.users[rec.Username]
		s.mu.RUnlock()
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "user_gone"})
			return
		}
		c.Set("user", u)
		c.Set("token", tok)
		c.Next()
	}
}

func currentUser(c *gin.Context) user {
	return c.MustGet("user").(user)
}

func (s *store) handleMe(c *gin.Context) {
	u := currentUser(c)
	c.JSON(http.StatusOK, u)
}

func (s *store) handleListExpenses(c *gin.Context) {
	u := currentUser(c)
	statusFilter := strings.TrimSpace(c.Query("status"))
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*expense, 0)
	for _, e := range s.expenses {
		if e.Submitter != u.Username {
			continue
		}
		if statusFilter != "" && string(e.Status) != statusFilter {
			continue
		}
		cp := *e
		list = append(list, &cp)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})
	c.JSON(http.StatusOK, gin.H{"items": list, "total": len(list)})
}

func (s *store) handleCreateExpense(c *gin.Context) {
	u := currentUser(c)
	var body struct {
		Title     string  `json:"title" binding:"required"`
		Category  string  `json:"category" binding:"required"`
		AmountCNY float64 `json:"amountCny" binding:"required"`
		Reason    string  `json:"reason" binding:"required"`
		// autoSubmit if true creates as SUBMITTED
		AutoSubmit bool `json:"autoSubmit"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body", "message": err.Error()})
		return
	}
	if body.AmountCNY <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_amount", "message": "amountCny must be > 0"})
		return
	}
	now := time.Now().UTC()
	e := &expense{
		ID: s.nextID("exp"), Title: body.Title, Category: body.Category,
		AmountCNY: body.AmountCNY, Currency: "CNY", Reason: body.Reason,
		Status: statusDraft, Submitter: u.Username, Approver: u.ManagerUsername,
		CreatedAt: now, UpdatedAt: now,
	}
	if body.AutoSubmit {
		if u.ManagerUsername == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no_manager", "message": "user has no manager for approval"})
			return
		}
		e.Status = statusSubmitted
		e.SubmittedAt = &now
	}
	s.mu.Lock()
	s.expenses[e.ID] = e
	s.mu.Unlock()
	c.JSON(http.StatusCreated, e)
}

func (s *store) handleGetExpense(c *gin.Context) {
	u := currentUser(c)
	id := c.Param("id")
	s.mu.RLock()
	e, ok := s.expenses[id]
	s.mu.RUnlock()
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	// Submitter or assigned approver may read.
	if e.Submitter != u.Username && e.Approver != u.Username {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	c.JSON(http.StatusOK, e)
}

func (s *store) handleSubmitExpense(c *gin.Context) {
	u := currentUser(c)
	id := c.Param("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.expenses[id]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	if e.Submitter != u.Username {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if e.Status != statusDraft {
		c.JSON(http.StatusConflict, gin.H{"error": "invalid_status", "message": "only DRAFT can be submitted", "status": e.Status})
		return
	}
	if u.ManagerUsername == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no_manager"})
		return
	}
	now := time.Now().UTC()
	e.Status = statusSubmitted
	e.Approver = u.ManagerUsername
	e.SubmittedAt = &now
	e.UpdatedAt = now
	c.JSON(http.StatusOK, e)
}

func (s *store) handlePendingApprovals(c *gin.Context) {
	u := currentUser(c)
	if u.Role != roleManager {
		c.JSON(http.StatusOK, gin.H{"items": []any{}, "total": 0, "message": "current user is not a manager"})
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*expense, 0)
	for _, e := range s.expenses {
		if e.Status == statusSubmitted && e.Approver == u.Username {
			cp := *e
			list = append(list, &cp)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})
	c.JSON(http.StatusOK, gin.H{"items": list, "total": len(list)})
}

func (s *store) handleDecideApproval(c *gin.Context) {
	u := currentUser(c)
	if u.Role != roleManager {
		c.JSON(http.StatusForbidden, gin.H{"error": "not_manager"})
		return
	}
	id := c.Param("id")
	var body struct {
		Decision string `json:"decision" binding:"required"` // APPROVE | REJECT
		Note     string `json:"note"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body", "message": err.Error()})
		return
	}
	decision := strings.ToUpper(strings.TrimSpace(body.Decision))
	if decision != "APPROVE" && decision != "REJECT" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_decision", "message": "decision must be APPROVE or REJECT"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.expenses[id]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	if e.Status != statusSubmitted || e.Approver != u.Username {
		c.JSON(http.StatusConflict, gin.H{
			"error": "not_pending_for_you", "status": e.Status, "approver": e.Approver,
		})
		return
	}
	now := time.Now().UTC()
	e.DecisionNote = body.Note
	e.DecidedAt = &now
	e.UpdatedAt = now
	if decision == "APPROVE" {
		e.Status = statusApproved
		// Deduct budget of submitter's department.
		if sub, ok := s.users[e.Submitter]; ok {
			if rem, ok := s.budgets[sub.Department]; ok {
				s.budgets[sub.Department] = rem - e.AmountCNY
			}
		}
	} else {
		e.Status = statusRejected
	}
	c.JSON(http.StatusOK, e)
}

func (s *store) handleBudgetSummary(c *gin.Context) {
	u := currentUser(c)
	s.mu.RLock()
	defer s.mu.RUnlock()
	remaining := s.budgets[u.Department]
	var pending float64
	var approved float64
	for _, e := range s.expenses {
		sub, ok := s.users[e.Submitter]
		if !ok || sub.Department != u.Department {
			continue
		}
		switch e.Status {
		case statusSubmitted:
			pending += e.AmountCNY
		case statusApproved:
			approved += e.AmountCNY
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"department":            u.Department,
		"currency":              "CNY",
		"remainingBudgetCny":    remaining,
		"pendingApprovalCny":    pending,
		"approvedThisPeriodCny": approved,
		"asOf":                  time.Now().UTC(),
	})
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// fallback non-crypto for mock only
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
