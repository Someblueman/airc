package auth

import "context"

// VerifyAccount verifies the provisioned token's exact non-guest account.
// It grants no room or event authority.
func (s *MatrixServer) VerifyAccount(parent context.Context) error {
	var account struct {
		UserID   string `json:"user_id"`
		DeviceID string `json:"device_id"`
		Guest    bool   `json:"is_guest"`
	}
	if s.getMatrixJSON(parent, "/_matrix/client/v3/account/whoami", &account, 16384) != nil {
		return ErrAuthentication
	}
	if account.UserID != s.account || account.Guest || parent.Err() != nil {
		return ErrAuthentication
	}
	return nil
}
