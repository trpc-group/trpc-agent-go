# Database

Flags `rows, _ := db.Query(...)` without a visible `rows.Close()` in the added
line. Process-owned `*sql.DB` values are not treated as leaks.
