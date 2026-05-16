package docs

import "github.com/swaggo/swag"

var SwaggerInfo = &swag.Spec{
	Version:          "1.0",
	Host:             "localhost:8080",
	BasePath:         "/",
	Schemes:          []string{"http"},
	Title:            "Dyson FileSystem API",
	Description:      "API documentation for Dyson FileSystem",
	InfoInstanceName: "swagger",
}

func init() {
	swag.Register(SwaggerInfo.InstanceName(), SwaggerInfo)
}
