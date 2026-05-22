import os
import json
import mysql.connector
import requests
from dotenv import load_dotenv

# Carregar variáveis do arquivo .env
load_dotenv()

# Configuração da API do TMDb
TMDB_API_KEY = os.getenv('TMDB_API_KEY')
TMDB_GENRES_URL = 'https://api.themoviedb.org/3/genre/movie/list'

# Caminho para o arquivo de configuração do banco de dados
DB_CONFIG_PATH = './db/db_config.json'

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

# Função para buscar gêneros de filmes da API do TMDb
def get_movie_genres():
    params = {
        'api_key': TMDB_API_KEY,
        'language': 'pt-BR'
    }
    response = requests.get(TMDB_GENRES_URL, params=params)
    if response.status_code == 200:
        return response.json().get('genres', [])
    else:
        print(f"Erro ao buscar os gêneros: {response.status_code}")
        return []

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

# Função para verificar se a categoria já existe no banco de dados
def category_exists(connection, category_name):
    cursor = connection.cursor(dictionary=True)
    query = "SELECT COUNT(*) as count FROM streams_categories WHERE category_name = %s"
    cursor.execute(query, (category_name,))
    result = cursor.fetchone()
    cursor.close()
    return result['count'] > 0

# Função principal para buscar e inserir categorias
def fetch_and_insert_categories():
    # Buscar gêneros da API do TMDb
    genres = get_movie_genres()

    if not genres:
        print("Nenhum gênero encontrado para inserir.")
        return

    # Conectar ao banco de dados
    connection = connect_to_database()

    # Inserir cada gênero como uma categoria
    for index, genre in enumerate(genres, start=1):
        if not category_exists(connection, genre['name']):
            category_data = {
                "category_type": "movie",
                "category_name": genre['name'],
                "parent_id": 0,
                "cat_order": index,
                "is_adult": 0  # Você pode ajustar se alguma categoria for considerada adulta
            }
            insert_category(connection, category_data)
            print(f"Categoria '{genre['name']}' inserida com sucesso.")
        else:
            print(f"Categoria '{genre['name']}' já existe. Pulando inserção.")

    # Fechar conexão com o banco
    connection.close()
    print("Processo concluído.")

# Executar o processo
fetch_and_insert_categories()
