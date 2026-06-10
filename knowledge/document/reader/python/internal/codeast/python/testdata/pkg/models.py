"""Data models for the package."""


class User:
    """Represents a user in the system."""

    def __init__(self, name: str, email: str):
        self.name = name
        self.email = email

    def display(self) -> str:
        """Return display name."""
        return f"{self.name} <{self.email}>"
