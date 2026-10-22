package authorentity

// PanicOnCredit makes the key of one credit panic until the returned func
// restores it.
func PanicOnCredit(id int64) (restore func()) {
	keyFunc = func(c *Credit) nameKey {
		if c.ID == id {
			panic("a catalog value no code path expected")
		}
		return keyOf(c)
	}
	return func() { keyFunc = keyOf }
}
