package handlers

// officialRegistrationRole keeps public registration from accepting an
// administrator-controlled role.
func officialRegistrationRole() string { return "viewer" }
