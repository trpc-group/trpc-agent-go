"""Service layer."""
from .models import User


class UserService:
    """Manages user operations."""

    def __init__(self):
        self.users = []

    def add_user(self, name: str, email: str) -> User:
        """Create and add a new user."""
        user = User(name, email)
        self.users.append(user)
        return user

    def find_user(self, name: str):
        """Find a user by name."""
        for u in self.users:
            if u.name == name:
                return u
        return None
