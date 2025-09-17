// Package config holds details like routing and other configs for the app
package config

type AppConfiger interface {
	GetPort() string
	GetDBURL() string
	GetControlPlaneURL() string
}
