import psycopg2
from psycopg2 import extensions

def setup_database():
    # Connection details
    db_host = "localhost"
    db_user = "postgres"
    db_password = "localhost"  # Replace with your actual password
    db_port = "5432"
    
    db_name = "reverseproxy"

    # --- STEP 1: ATTEMPT TO CREATE THE DATABASE ---
    db_exists = False
    conn = None
    cursor = None
    
    try:
        # Connect to default system DB to check/create the target DB
        conn = psycopg2.connect(
            host=db_host, user=db_user, password=db_password, port=db_port, database="postgres"
        )
        conn.set_isolation_level(extensions.ISOLATION_LEVEL_AUTOCOMMIT)
        cursor = conn.cursor()

        # Check if DB exists
        cursor.execute(f"SELECT 1 FROM pg_catalog.pg_database WHERE datname = '{db_name}';")
        if not cursor.fetchone():
            cursor.execute(f"CREATE DATABASE {db_name};")
            print(f"Database '{db_name}' created successfully.")
        else:
            print(f"Database '{db_name}' already exists. Proceeding to table check...")

    except Exception as error:
        print(f"Notice during DB creation step: {error}")
        print("Will still attempt to connect and verify the table...")
    finally:
        if cursor: cursor.close()
        if conn: conn.close()

    # --- STEP 2: ALWAYS TRY TO CREATE THE TABLE REGARDLESS ---
    try:
        # Connect directly to your target database
        conn = psycopg2.connect(
            host=db_host, user=db_user, password=db_password, port=db_port, database=db_name
        )
        cursor = conn.cursor()

        # SQL query using IF NOT EXISTS
        create_table_query = """
        CREATE TABLE IF NOT EXISTS users (
            id SERIAL PRIMARY KEY,
            username VARCHAR(50) UNIQUE NOT NULL,
            password VARCHAR(255) NOT NULL,
            email VARCHAR(100) UNIQUE NOT NULL,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        );
        """
        
        cursor.execute(create_table_query)
        conn.commit()  # Save changes
        print("Table 'users' verified/created successfully.")

    except Exception as error:
        print(f"Error creating table: {error}")
    finally:
        if cursor: cursor.close()
        if conn: conn.close()

if __name__ == "__main__":
    setup_database()
