module github.com/levonn-dev/vgkeep/libs/go/jwtauthtest

go 1.26.2

replace github.com/levonn-dev/vgkeep/libs/go/jwtauth => ../jwtauth

require (
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/uuid v1.6.0
	github.com/levonn-dev/vgkeep/libs/go/jwtauth v0.0.0-00010101000000-000000000000
)
