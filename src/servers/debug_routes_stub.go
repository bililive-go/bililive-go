//go:build !dev

package servers

import "github.com/gorilla/mux"

func registerDevDebugRoutes(apiRoute *mux.Router) {}
