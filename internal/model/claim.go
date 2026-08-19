package model

// ClaimState tracks what the user has actually done about a settlement. The
// point of keeping it is that a class action pays months or years after the
// claim goes in, by which time nobody remembers whether they filed.
type ClaimState string

const (
	StateNew        ClaimState = "new"
	StateInterested ClaimState = "interested"
	StateFiled      ClaimState = "filed"
	StatePaid       ClaimState = "paid"
	StateRejected   ClaimState = "rejected"
	StateSkipped    ClaimState = "skipped"
)

// Claim is the user's side of one settlement.
type Claim struct {
	SettlementID string     `json:"settlement_id"`
	State        ClaimState `json:"state"`
	FiledOn      Date       `json:"filed_on"`
	PaidOn       Date       `json:"paid_on"`
	Amount       float64    `json:"amount"`
	Reference    string     `json:"reference,omitempty"` // confirmation number
	Note         string     `json:"note,omitempty"`
	UpdatedOn    Date       `json:"updated_on"`
}

// Open reports whether the claim still needs attention from the user.
func (c *Claim) Open() bool {
	return c.State == StateNew || c.State == StateInterested
}
