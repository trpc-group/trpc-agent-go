# SQL Injection Prevention Guide

Language-agnostic SQL injection prevention strategies with cross-language code examples.

> **Related**: [Security Review Guide](../security-review-guide.md) for comprehensive security checklist and decision framework.

## Attack Types

SQL injection (SQLi) is ranked #3 in the OWASP Top 10 (2021). Three common variants:

| Type | Description | Risk |
|------|-------------|------|
| **Classic (In-band)** | Attacker receives results directly in the HTTP response | Data exfiltration, authentication bypass |
| **Blind (Boolean/Time-based)** | Attacker infers data from response differences or timing | Slower but still viable for data extraction |
| **Out-of-band** | Attacker uses DNS/HTTP callbacks to exfiltrate data | Less common but harder to detect |

## Universal Prevention Strategy

1. **Parameterized queries** — always (the #1 defense)
2. **ORM safe usage** — understand what your ORM escapes
3. **Input validation** — whitelist over blacklist
4. **Least privilege** — database user with minimal permissions
5. **WAF** — web application firewall as defense-in-depth

---

## Cross-Language Examples

### Python

```python
# ❌ Vulnerable: string formatting
query = f"SELECT * FROM users WHERE id = {user_id}"
cursor.execute(query)

# ❌ Vulnerable: % formatting
cursor.execute("SELECT * FROM users WHERE id = %s" % user_id)

# ✅ Parameterized (DB-API)
cursor.execute("SELECT * FROM users WHERE id = %s", (user_id,))

# ✅ SQLAlchemy ORM
User.query.filter(User.id == user_id).all()

# ❌ SQLAlchemy raw SQL with string interpolation
session.execute(text(f"SELECT * FROM users WHERE id = {user_id}"))

# ✅ SQLAlchemy raw SQL with bound parameters
session.execute(text("SELECT * FROM users WHERE id = :id"), {"id": user_id})

# ✅ Django ORM
User.objects.filter(id=user_id)

# ❌ Django extra() with string interpolation
User.objects.extra(where=[f"username = '{username}'"])

# ✅ Django raw() with parameters
User.objects.raw("SELECT * FROM users WHERE id = %s", [user_id])
```

### Java

```java
// ❌ Vulnerable: string concatenation
String query = "SELECT * FROM users WHERE id = " + userId;
Statement stmt = connection.createStatement();
ResultSet rs = stmt.executeQuery(query);

// ✅ JDBC PreparedStatement
String query = "SELECT * FROM users WHERE id = ?";
PreparedStatement stmt = connection.prepareStatement(query);
stmt.setLong(1, userId);
ResultSet rs = stmt.executeQuery();

// ✅ JPA parameter binding
@Query("SELECT u FROM User u WHERE u.id = :id")
User findById(@Param("id") Long id);

// ✅ Spring Data JPA method naming
User findById(Long id);

// ❌ JPA native query with string concatenation
entityManager.createNativeQuery(
    "SELECT * FROM users WHERE name = '" + name + "'"
);

// ✅ JPA native query with parameter binding
Query query = entityManager.createNativeQuery(
    "SELECT * FROM users WHERE name = :name"
);
query.setParameter("name", name);
```

### Go

```go
// ❌ Vulnerable: fmt.Sprintf
query := fmt.Sprintf("SELECT * FROM users WHERE id = %s", userID)
rows, err := db.Query(query)

// ✅ database/sql parameterized
rows, err := db.Query("SELECT * FROM users WHERE id = ?", userID)

// ✅ Named parameters (sqlx)
rows, err := db.NamedQuery(
    "SELECT * FROM users WHERE id = :id",
    map[string]interface{}{"id": userID},
)

// ⚠️ Dynamic identifiers (table/column names) can't use placeholders
// Must validate against whitelist
var allowedColumns = map[string]bool{
    "id": true, "name": true, "email": true, "created_at": true,
}

func queryWithOrder(db *sql.DB, orderBy string) (*sql.Rows, error) {
    if !allowedColumns[orderBy] {
        return nil, fmt.Errorf("invalid column: %s", orderBy)
    }
    return db.Query(
        fmt.Sprintf("SELECT * FROM users ORDER BY %s", orderBy),
    )
}
```

### Node.js

```typescript
// ❌ Vulnerable: template literal
const query = `SELECT * FROM users WHERE id = ${userId}`;
const result = await client.query(query);

// ✅ pg parameterized ($1, $2, ...)
const result = await client.query(
    "SELECT * FROM users WHERE id = $1",
    [userId]
);

// ✅ Prisma ORM (parameterized by default)
const user = await prisma.user.findUnique({
    where: { id: userId },
});

// ❌ Prisma $queryRawUnsafe with string interpolation
await prisma.$queryRawUnsafe(
    `SELECT * FROM users WHERE id = ${userId}`
);

// ✅ Prisma $queryRaw with tagged template (safe)
await prisma.$queryRaw`
    SELECT * FROM users WHERE id = ${userId}
`;
```

### PHP

```php
<?php

// ❌ Vulnerable: string concatenation
$sql = "SELECT * FROM users WHERE email = '" . $_GET['email'] . "'";
$user = $pdo->query($sql)->fetch();

// ✅ PDO prepared statements
$stmt = $pdo->prepare("SELECT * FROM users WHERE email = :email");
$stmt->execute(['email' => $email]);
$user = $stmt->fetch(PDO::FETCH_ASSOC);

// ✅ PDO positional placeholders
$stmt = $pdo->prepare("SELECT * FROM users WHERE id = ?");
$stmt->execute([$id]);

// ❌ mysqli with string interpolation
$result = mysqli_query($conn,
    "SELECT * FROM users WHERE id = " . $id
);

// ✅ mysqli prepared statements
$stmt = mysqli_prepare($conn, "SELECT * FROM users WHERE id = ?");
mysqli_stmt_bind_param($stmt, "i", $id);
mysqli_stmt_execute($stmt);

// ✅ Laravel Eloquent ORM
User::where('id', $id)->first();

// ❌ Laravel DB::raw with interpolation
DB::select(DB::raw("SELECT * FROM users WHERE id = {$id}"));

// ✅ Laravel parameterized raw
DB::select("SELECT * FROM users WHERE id = ?", [$id]);
```

### C# / .NET

```csharp
// ❌ Vulnerable: string concatenation
var query = $"SELECT * FROM Users WHERE Id = {userId}";
using var cmd = new SqlCommand(query, connection);
var reader = cmd.ExecuteReader();

// ✅ ADO.NET parameterized
var query = "SELECT * FROM Users WHERE Id = @Id";
using var cmd = new SqlCommand(query, connection);
cmd.Parameters.AddWithValue("@Id", userId);

// ✅ Dapper parameterized
var users = connection.Query<User>(
    "SELECT * FROM Users WHERE Id = @Id",
    new { Id = userId }
);

// ❌ Dapper with string interpolation
var users = connection.Query<User>(
    $"SELECT * FROM Users WHERE Id = {userId}"
);

// ✅ EF Core (parameterized by default)
var user = await context.Users
    .Where(u => u.Id == userId)
    .FirstOrDefaultAsync();

// ❌ EF Core FromSqlRaw with interpolation
var users = context.Users
    .FromSqlRaw($"SELECT * FROM Users WHERE Id = {userId}")
    .ToList();

// ✅ EF Core FromSql with FormattableString (parameterized)
var users = context.Users
    .FromSql($"SELECT * FROM Users WHERE Id = {userId}")
    .ToList();
```

---

## ORM Unsafe Usage Patterns

ORMs do NOT automatically prevent SQL injection in all cases:

```python
# ❌ SQLAlchemy: text() with f-string
session.execute(text(f"SELECT * FROM users WHERE id = {user_id}"))

# ❌ Django: extra() / RawSQL() with string interpolation
User.objects.extra(where=[f"username = '{username}'"])
User.objects.annotate(
    val=RawSQL(f"SELECT col FROM other WHERE id = {user_id}")
)

# ❌ JPA: createNativeQuery with string concatenation
entityManager.createNativeQuery("SELECT * FROM users WHERE name = '" + name + "'")

# ❌ EF Core: FromSqlRaw with string interpolation
context.Users.FromSqlRaw($"SELECT * FROM Users WHERE Id = {userId}")
```

**Rule**: Every ORM has a "raw SQL" escape hatch. String interpolation in that escape hatch = SQL injection. Always use the ORM's parameter binding mechanism.

---

## Dynamic Identifiers (Table/Column Names)

Placeholders can only bind **values**, not table names, column names, or SQL keywords. For dynamic identifiers:

```python
# ✅ Whitelist validation
ALLOWED_COLUMNS = {"id", "name", "email", "created_at"}
ALLOWED_DIRECTIONS = {"ASC", "DESC"}

def get_users(order_by: str, direction: str) -> list[User]:
    if order_by not in ALLOWED_COLUMNS:
        raise ValueError(f"Invalid column: {order_by}")
    if direction.upper() not in ALLOWED_DIRECTIONS:
        raise ValueError(f"Invalid direction: {direction}")

    return User.objects.order_by(
        f"{'-' if direction.upper() == 'DESC' else ''}{order_by}"
    )
```

---

## Detection & Testing

```bash
# Automated scanning
sqlmap -u "https://example.com/api/users?id=1" --batch

# Static analysis (Python)
bandit -r src/ -f custom

# Static analysis (Java)
spotbugs -textui build/classes

# Code review keywords to search for
grep -rn "f\".*SELECT\|f'.*SELECT\|fmt.Sprintf.*SELECT\|format.*SELECT" src/
grep -rn "query.*\+.*\|query.*&\|query.*concat" src/
```

---

## Review Checklist

- [ ] All SQL queries use parameterized queries (no string interpolation)
- [ ] ORM raw SQL methods use bound parameters, not string formatting
- [ ] Dynamic identifiers (table/column names) validated against whitelist
- [ ] Database user has least privilege (no DROP/ALTER for app user)
- [ ] No SQL queries constructed from user input without parameterization
- [ ] Static analysis tools (Bandit, SpotBugs, SonarQube) run in CI