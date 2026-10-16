package auth

import (
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

const tokenInfoUser = "mirage.user"

// UserFromTokenInfo returns the user a request authenticated as, from the token info the
// SDK's bearer middleware attaches to it.
func UserFromTokenInfo(ti *sdkauth.TokenInfo) (User, bool) {
	if ti == nil {
		return User{}, false
	}
	u, ok := ti.Extra[tokenInfoUser].(User)
	return u, ok
}
