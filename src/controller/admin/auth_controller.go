package admin

import (
	"errors"

	"github.com/assimon/luuu/model/data"
	"github.com/assimon/luuu/model/mdb"
	appjwt "github.com/assimon/luuu/util/jwt"
	"github.com/labstack/echo/v4"
)

// LoginRequest is the payload for admin login.
type LoginRequest struct {
	Username string `json:"username" validate:"required" example:"admin"`
	Password string `json:"password" validate:"required" example:"password123"`
}

// LoginResponse is the response for a successful login.
type LoginResponse struct {
	Token    string `json:"token" example:"eyJhbGciOiJIUzI1NiIs..."`
	Username string `json:"username" example:"admin"`
	UserID   uint64 `json:"user_id" example:"1"`
}

// ChangePasswordRequest is the payload for changing admin password.
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" validate:"required" example:"old_pass"`
	NewPassword string `json:"new_password" validate:"required|minLen:6" example:"new_pass123"`
}

// MeResponse wraps AdminUser with a default-password flag.
type MeResponse struct {
	mdb.AdminUser
	PasswordIsDefault bool `json:"password_is_default" example:"true"`
}

// Login verifies credentials, stamps last_login_at, returns a signed JWT.
// @Summary      Admin login
// @Description  Verify credentials and return a signed JWT token
// @Tags         Admin Auth
// @Accept       json
// @Produce      json
// @Param        request body admin.LoginRequest true "Login credentials"
// @Success      200 {object} response.ApiResponse{data=admin.LoginResponse}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/auth/login [post]
func (c *BaseAdminController) Login(ctx echo.Context) error {
	req := new(LoginRequest)
	if err := ctx.Bind(req); err != nil {
		return c.FailJson(ctx, err)
	}
	if err := c.ValidateStruct(ctx, req); err != nil {
		return c.FailJson(ctx, err)
	}
	user, err := data.GetAdminUserByUsername(req.Username)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	if user.ID == 0 || !data.VerifyPassword(user.PasswordHash, req.Password) {
		return c.FailJson(ctx, errors.New("invalid username or password"))
	}
	if user.Status != mdb.AdminUserStatusEnable {
		return c.FailJson(ctx, errors.New("user disabled"))
	}
	token, err := appjwt.Sign(user.ID, user.Username)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	err = data.TouchAdminUserLastLogin(user.ID)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, LoginResponse{
		Token:    token,
		Username: user.Username,
		UserID:   user.ID,
	})
}

// Logout is a no-op stub — tokens are stateless and the frontend
// discards them. Kept for API symmetry with the documented spec.
// @Summary      Admin logout
// @Description  Logout (no-op, frontend discards token)
// @Tags         Admin Auth
// @Security     AdminJWT
// @Produce      json
// @Success      200 {object} response.ApiResponse
// @Router       /admin/api/v1/auth/logout [post]
func (c *BaseAdminController) Logout(ctx echo.Context) error {
	return c.SucJson(ctx, nil)
}

// Me returns the currently authenticated admin user profile.
// @Summary      Get current admin profile
// @Description  Returns the currently authenticated admin user
// @Tags         Admin Auth
// @Security     AdminJWT
// @Produce      json
// @Success      200 {object} response.ApiResponse{data=admin.MeResponse}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/auth/me [get]
func (c *BaseAdminController) Me(ctx echo.Context) error {
	uid := currentAdminUserID(ctx)
	if uid == 0 {
		return c.FailJson(ctx, errors.New("unauthorized"))
	}
	user, err := data.GetAdminUserByID(uid)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	if user.ID == 0 {
		return c.FailJson(ctx, errors.New("user not found"))
	}
	// Warn the frontend when the operator hasn't changed the default
	// password so the UI can show a prominent reminder.
	isDefault := data.VerifyPassword(user.PasswordHash, "admin")
	return c.SucJson(ctx, MeResponse{
		AdminUser:         *user,
		PasswordIsDefault: isDefault,
	})
}

// ChangePassword requires the old password, updates to the new one.
// @Summary      Change admin password
// @Description  Change the current admin user's password
// @Tags         Admin Auth
// @Security     AdminJWT
// @Accept       json
// @Produce      json
// @Param        request body admin.ChangePasswordRequest true "Password payload"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/auth/password [post]
func (c *BaseAdminController) ChangePassword(ctx echo.Context) error {
	uid := currentAdminUserID(ctx)
	if uid == 0 {
		return c.FailJson(ctx, errors.New("unauthorized"))
	}
	req := new(ChangePasswordRequest)
	if err := ctx.Bind(req); err != nil {
		return c.FailJson(ctx, err)
	}
	if err := c.ValidateStruct(ctx, req); err != nil {
		return c.FailJson(ctx, err)
	}
	user, err := data.GetAdminUserByID(uid)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	if user.ID == 0 || !data.VerifyPassword(user.PasswordHash, req.OldPassword) {
		return c.FailJson(ctx, errors.New("old password incorrect"))
	}
	if err := data.UpdateAdminUserPassword(uid, req.NewPassword); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, nil)
}
