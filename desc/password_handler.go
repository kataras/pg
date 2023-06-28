package desc

// PasswordHandler is a type that represents a password handler for the database.
type PasswordHandler struct {
	// Encrypt takes a table name and a plain password as strings and returns an encrypted password as a string.
	Encrypt func(tableName, plainPassword string) (encryptedPassword string, err error)
	// Decrypt takes a table name and an encrypted password as strings and returns a plain password as a string.
	Decrypt func(tableName, encryptedPassword string) (plainPassword string, err error)
}

func (h *PasswordHandler) canEncrypt() bool {
	if h == nil {
		return false
	}

	return h.Encrypt != nil
}

func (h *PasswordHandler) canDecrypt() bool {
	if h == nil {
		return false
	}

	return h.Decrypt != nil
}
