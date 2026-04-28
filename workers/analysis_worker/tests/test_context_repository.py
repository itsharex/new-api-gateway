from context_repository import PostgresContextRepository


class FakeCursor:
    def __init__(self):
        self.executed = []
        self.rows = [
            (
                1,
                "repo",
                "new-api-gateway",
                "Audit gateway",
                ["new-api", "gateway"],
                ["audit gateway"],
                "platform",
                ["coding", "debugging"],
                ["gpt-4.1"],
                "normal",
                True,
            )
        ]

    def execute(self, query, params=None):
        self.executed.append((query, params))

    def fetchall(self):
        return self.rows


class FakeConnection:
    def __init__(self):
        self.cursor_obj = FakeCursor()

    def cursor(self):
        return self.cursor_obj


def test_list_active_contexts_returns_catalog_entries():
    conn = FakeConnection()
    repo = PostgresContextRepository(conn)

    contexts = repo.list_active_contexts()

    assert contexts[0].name == "new-api-gateway"
    assert contexts[0].search_terms() == ["new-api", "gateway", "audit gateway", "new-api-gateway"]
    query = conn.cursor_obj.executed[0][0]
    assert "FROM context_catalog" in query
    assert "WHERE active = TRUE" in query
