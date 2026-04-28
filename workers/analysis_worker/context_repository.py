from models import ContextCatalogEntry


class PostgresContextRepository:
    def __init__(self, connection):
        self.connection = connection

    def list_active_contexts(self) -> list[ContextCatalogEntry]:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            SELECT
                id, context_type, name, description, keywords, aliases, owner,
                expected_task_categories, expected_models, expected_usage_level, active
            FROM context_catalog
            WHERE active = TRUE
            ORDER BY context_type, name
            """
        )
        return [
            ContextCatalogEntry(
                id=row[0],
                context_type=row[1],
                name=row[2],
                description=row[3],
                keywords=list(row[4] or []),
                aliases=list(row[5] or []),
                owner=row[6],
                expected_task_categories=list(row[7] or []),
                expected_models=list(row[8] or []),
                expected_usage_level=row[9],
                active=row[10],
            )
            for row in cursor.fetchall()
        ]
