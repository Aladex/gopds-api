package models

// InviteRequest request for update, delete or create invite for registration
type InviteRequest struct {
	Action string `json:"action" form:"action"`
	Invite Invite `json:"invite" form:"invite"`
}
