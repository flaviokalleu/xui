import os
import json
import mysql.connector
import requests
from dotenv import load_dotenv

# Carregar variáveis do arquivo .env
load_dotenv()

# Configuração da API do TMDb
TMDB_API_KEY = os.getenv('apikey')
TMDB_GENRES_URL = 'https://api.themoviedb.org/3/genre/movie/list'

# Caminho para o arquivo de configuração do banco de dados
DB_CONFIG_PATH = './db/db_config.json'

# Dicionário de providers
providers = {
    # Serviços principais
    'netflix': {'name': 'Netflix', 'id': 8},
    'prime video': {'name': 'Amazon Prime', 'id': 119},
    'prime': {'name': 'Amazon Prime', 'id': 119},
    'amazon prime': {'name': 'Amazon Prime', 'id': 119},
    'amazon': {'name': 'Amazon Prime', 'id': 119},
    'amazon video': {'name': 'Amazon Prime', 'id': 119},
    
    # Disney e relacionados
    'disney': {'name': 'Disney Plus', 'id': 337},
    'disney plus': {'name': 'Disney Plus', 'id': 337},
    'disney+': {'name': 'Disney Plus', 'id': 337},
    'star plus': {'name': 'Star Plus', 'id': 619},
    'star+': {'name': 'Star Plus', 'id': 619},
    
    # HBO
    'hbo max': {'name': 'HBO Max', 'id': 384},
    'hbo': {'name': 'HBO Max', 'id': 384},
    'hbomax': {'name': 'HBO Max', 'id': 384},
    
    # Paramount
    'paramount plus': {'name': 'Paramount Plus', 'id': 531},
    'paramount+': {'name': 'Paramount Plus', 'id': 531},
    'paramount': {'name': 'Paramount Plus', 'id': 531},
    
    # Apple
    'apple tv plus': {'name': 'Apple TV Plus', 'id': 350},
    'apple tv+': {'name': 'Apple TV Plus', 'id': 350},
    'apple tv': {'name': 'Apple TV Plus', 'id': 350},
    'apple': {'name': 'Apple TV Plus', 'id': 350},
    
    # Globo
    'globoplay': {'name': 'Globoplay', 'id': 307},
    'globo': {'name': 'Globoplay', 'id': 307},
    'globo play': {'name': 'Globoplay', 'id': 307},
    
    # Discovery
    'discovery plus': {'name': 'Discovery+', 'id': 520},
    'discovery+': {'name': 'Discovery+', 'id': 520},
    'discovery': {'name': 'Discovery+', 'id': 520},
    
    # Doramas (substituição de Viki/Rakuten)
    'doramas': {'name': 'Doramas', 'id': 344},
    
    # Crunchyroll/Funimation
    'crunchyroll': {'name': 'Crunchyroll/Funimation', 'id': 356},
    'funimation': {'name': 'Crunchyroll/Funimation', 'id': 356},
    'crunchy': {'name': 'Crunchyroll/Funimation', 'id': 356},
    
    # Claro
    'claro tv': {'name': 'Claro TV+', 'id': 621},
    'claro tv+': {'name': 'Claro TV+', 'id': 621},
    'claro': {'name': 'Claro TV+', 'id': 621}    
}

# Função para conectar ao banco de dados
def connect_to_database():
    with open(DB_CONFIG_PATH, 'r') as file:
        config = json.load(file)
    return mysql.connector.connect(
        host=config['host'],
        user=config['user'],
        password=config['password'],
        database=config['database']
    )

# Função para verificar se a categoria já existe no banco de dados
def category_exists(connection, category_name):
    cursor = connection.cursor(dictionary=True)
    query = "SELECT COUNT(*) as count FROM streams_categories WHERE category_name = %s"
    cursor.execute(query, (category_name,))
    result = cursor.fetchone()
    cursor.close()
    return result['count'] > 0

# Função para inserir categorias no banco de dados
def insert_category(connection, category_data):
    cursor = connection.cursor()
    query = """
        INSERT INTO streams_categories (
            category_type, category_name, parent_id, cat_order, is_adult
        ) VALUES (%(category_type)s, %(category_name)s, %(parent_id)s, %(cat_order)s, %(is_adult)s)
    """
    cursor.execute(query, category_data)
    connection.commit()
    cursor.close()

# Função principal para associar providers às categorias de séries
def associate_providers_to_series():
    connection = connect_to_database()
    
    for provider, details in providers.items():
        if not category_exists(connection, details['name']):
            category_data = {
                "category_type": "series",
                "category_name": details['name'],
                "parent_id": 0,
                "cat_order": details['id'],  # Assumindo ID fixo, ajustar conforme necessário
                "is_adult": 0
            }
            insert_category(connection, category_data)
            print(f"Provider '{details['name']}' associado com sucesso.")
        else:
            print(f"Provider '{details['name']}' já está associado.")

    connection.close()
    print("Associação de providers concluída.")

# Executar o processo
associate_providers_to_series()