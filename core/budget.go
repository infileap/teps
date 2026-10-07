package core

import "sync"

// A7: a budget is explicit, never implied.
//
// Cost is not a property of the work; it is a property of the decision to do
// the work. Carrying it in the open is what lets a caller refuse to start
// rather than discover the overrun halfway through.
type Budget struct {
	MS     int64 `json:"ms"`
	Tokens int64 `json:"tokens"`
	Money  int64 `json:"money"`
}

// Account is the only thing in the kernel that holds a mutable value.
//
// It is here for exactly one reason: the check and the subtraction have to be
// one indivisible step. Anywhere else, that could be arranged with a queue.
// Here it cannot, so it stays.
type Account struct {
	mu   sync.Mutex
	left Budget
}

func NewAccount(initial Budget) *Account {
	return &Account{left: initial}
}

// Spend takes want from the account, or refuses without taking anything.
//
// All three dimensions are checked before any of them is reduced, so a refusal
// never leaves a partial charge behind.
func (a *Account) Spend(want Budget) *Refusal {
	a.mu.Lock()
	defer a.mu.Unlock()
	if want.MS > a.left.MS || want.Tokens > a.left.Tokens || want.Money > a.left.Money {
		return Refuse(CodeBudgetExhausted, "requested budget exceeds what remains")
	}
	a.left.MS -= want.MS
	a.left.Tokens -= want.Tokens
	a.left.Money -= want.Money
	return nil
}

// Left reports what remains.
func (a *Account) Left() Budget {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.left
}
