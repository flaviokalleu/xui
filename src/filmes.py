import os
import re
import pandas as pd
import requests
import json
import mysql.connector
import logging
from dotenv import load_dotenv
from datetime import datetime
from typing import Optional, Tuple, List, Dict, Any
from mysql.connector.cursor import MySQLCursor
from mysql.connector import MySQLConnection

# Configurar logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s',
    handlers=[
        logging.FileHandler('movie_import.log'),
        logging.StreamHandler()
    ]
)

# Carregar variáveis do arquivo .env
load_dotenv()

# Configuração da API do TMDb
TMDB_API_KEY = os.getenv('TMDB_API_KEY')
TMDB_API_URL = 'https://api.themoviedb.org/3/movie/{}'
TMDB_GENRES_URL = 'https://api.themoviedb.org/3/genre/movie/list'

# Caminhos dos arquivos
DB_CONFIG_PATH = './db/db_config.json'
EXCEL_FILE_PATH = 'excel/videos.xlsx'

class DatabaseConnection:
    def __init__(self):
        self.connection = None
        
    def __enter__(self) -> MySQLConnection:
        try:
            with open(DB_CONFIG_PATH, 'r') as file:
                config = json.load(file)
            self.connection = mysql.connector.connect(
                host=config['host'],
                user=config['user'],
                password=config['password'],
                database=config['database']
            )
            return self.connection
        except Exception as e:
            logging.error(f"Erro ao conectar ao banco de dados: {str(e)}")
            raise
            
    def __exit__(self, exc_type, exc_val, exc_tb):
        if self.connection:
            self.connection.close()

def get_movie_credits(tmdb_id: int) -> Tuple[str, str]:
    """
    Obtém os créditos (diretor e elenco) de um filme do TMDB.
    """
    try:
        url = f'https://api.themoviedb.org/3/movie/{tmdb_id}/credits'
        params = {
            'api_key': TMDB_API_KEY,
            'language': 'pt-BR'
        }
        response = requests.get(url, params=params)
        response.raise_for_status()
        
        data = response.json()
        director = ', '.join([person['name'] for person in data.get('crew', []) if person['job'] == 'Director'])
        cast = ', '.join([person['name'] for person in data.get('cast', [])[:5]])
        return director, cast
    except Exception as e:
        logging.error(f"Erro ao obter créditos do filme {tmdb_id}: {str(e)}")
        return '', ''

def get_movie_details(tmdb_id: int) -> Optional[Dict[str, Any]]:
    """
    Obtém os detalhes de um filme do TMDB.
    """
    try:
        url = TMDB_API_URL.format(tmdb_id)
        params = {
            'api_key': TMDB_API_KEY,
            'language': 'pt-BR'
        }
        response = requests.get(url, params=params)
        response.raise_for_status()
        return response.json()
    except Exception as e:
        logging.error(f"Erro ao buscar detalhes do filme {tmdb_id}: {str(e)}")
        return None

def get_category_ids(cursor: MySQLCursor, genre_names: List[str]) -> List[str]:
    """
    Obtém os IDs das categorias com base nos nomes dos gêneros.
    """
    try:
        category_ids = []
        for genre in genre_names:
            query = "SELECT id FROM streams_categories WHERE category_name = %s"
            cursor.execute(query, (genre,))
            result = cursor.fetchone()
            if result:
                category_ids.append(str(result['id']))
        return category_ids if category_ids else ['40']
    except Exception as e:
        logging.error(f"Erro ao obter IDs das categorias: {str(e)}")
        return ['40']

def check_if_movie_exists(cursor: MySQLCursor, tmdb_id: int) -> bool:
    """
    Verifica se um filme já existe no banco de dados.
    """
    try:
        query = "SELECT id FROM streams WHERE tmdb_id = %s"
        cursor.execute(query, (tmdb_id,))
        return cursor.fetchone() is not None
    except Exception as e:
        logging.error(f"Erro ao verificar existência do filme {tmdb_id}: {str(e)}")
        return False

def get_or_create_movies_bouquet(cursor: MySQLCursor, connection: MySQLConnection) -> int:
    """
    Verifica se o bouquet FILMES existe e o cria caso não exista.
    Retorna o ID do bouquet.
    """
    try:
        # Verificar se já existe um bouquet com nome FILMES
        query = "SELECT id FROM bouquets WHERE bouquet_name = 'FILMES'"
        cursor.execute(query)
        result = cursor.fetchone()
        
        if result:
            bouquet_id = result['id']
            logging.info(f"Bouquet FILMES encontrado com ID {bouquet_id}")
            return bouquet_id
        else:
            # Criar o bouquet FILMES
            insert_query = """
                INSERT INTO bouquets (bouquet_name, bouquet_channels, bouquet_movies, 
                                     bouquet_radios, bouquet_series, bouquet_order)
                VALUES ('FILMES', '[]', '[]', '[]', '[]', 1)
            """
            cursor.execute(insert_query)
            connection.commit()
            
            bouquet_id = cursor.lastrowid
            logging.info(f"Bouquet FILMES criado com ID {bouquet_id}")
            return bouquet_id
    except Exception as e:
        logging.error(f"Erro ao obter ou criar bouquet FILMES: {str(e)}")
        raise

def update_bouquet_with_ids(cursor: MySQLCursor, bouquet_id: int, stream_ids: List[int]):
    """
    Atualiza o bouquet com todos os stream_ids inseridos.
    """
    try:
        query = "SELECT bouquet_movies FROM bouquets WHERE id = %s"
        cursor.execute(query, (bouquet_id,))
        result = cursor.fetchone()
        
        if result and result['bouquet_movies']:
            try:
                current_movies = json.loads(result['bouquet_movies'])
                if not isinstance(current_movies, list):
                    current_movies = []
            except (json.JSONDecodeError, TypeError):
                current_movies = []
        else:
            current_movies = []
                
        # Garantir que todos os IDs da lista estão no bouquet
        for stream_id in stream_ids:
            if stream_id not in current_movies:
                current_movies.append(stream_id)
                
        update_query = "UPDATE bouquets SET bouquet_movies = %s WHERE id = %s"
        cursor.execute(update_query, (json.dumps(current_movies), bouquet_id))
        
    except Exception as e:
        logging.error(f"Erro ao atualizar bouquet {bouquet_id} com IDs {stream_ids}: {str(e)}")
        raise

def insert_into_database(connection: MySQLConnection, movie_data: Dict[str, Any]) -> int:
    """
    Insere um novo filme no banco de dados.
    """
    cursor = connection.cursor()
    try:
        # Query principal para inserção do stream
        query = """
            INSERT INTO streams (
                type, category_id, stream_display_name, stream_source, stream_icon, notes,
                enable_transcode, transcode_attributes, custom_ffmpeg, movie_properties, movie_subtitles,
                read_native, target_container, stream_all, remove_subtitles, custom_sid, epg_api, epg_id,
                channel_id, epg_lang, `order`, auto_restart, transcode_profile_id, gen_timestamps, added,
                series_no, direct_source, tv_archive_duration, tv_archive_server_id, tv_archive_pid,
                vframes_server_id, vframes_pid, movie_symlink, rtmp_output, allow_record, probesize_ondemand,
                custom_map, external_push, delay_minutes, tmdb_language, llod, year, rating, plex_uuid, uuid,
                epg_offset, updated, similar, tmdb_id, adaptive_link, title_sync, fps_restart, fps_threshold,
                direct_proxy
            ) VALUES (
                2, %s, %s, %s, NULL, NULL, 0, NULL, NULL, %s, NULL, 0, 'mp4', 0, 0, NULL, 0, NULL, NULL, NULL,
                NULL, NULL, 0, 1, %s, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 128000, NULL, NULL, 0, 'pt-BR', 0, %s, %s,
                '\'\'', NULL, 0, %s, NULL, %s, NULL, NULL, 0, 90, 1
            )
        """
        
        # Corrigir formato da URL
        stream_source = movie_data['stream_source'].replace('\\\\/', '\\/')
        
        cursor.execute(query, (
            movie_data['category_id'],
            movie_data['stream_display_name'],
            stream_source,
            movie_data['movie_properties'],
            movie_data['added'],
            movie_data['year'],
            movie_data['rating'],
            movie_data['updated'],
            movie_data['tmdb_id']
        ))
        
        stream_id = cursor.lastrowid
        
        # Inserir na tabela streams_servers
        server_query = """
            INSERT INTO streams_servers (
                stream_id, server_id, parent_id, pid, to_analyze, stream_status,
                stream_started, stream_info, monitor_pid, aes_pid, current_source,
                bitrate, progress_info, cc_info, on_demand, delay_pid,
                delay_available_at, pids_create_channel, cchannel_rsources,
                updated, compatible, audio_codec, video_codec, resolution, ondemand_check
            ) VALUES (
                %s, 1, NULL, NULL, 0, 0, NULL, NULL, NULL, NULL, NULL, NULL,
                NULL, NULL, 0, NULL, NULL, NULL, NULL, %s, 0, NULL, NULL, NULL, NULL
            )
        """
        current_time = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
        cursor.execute(server_query, (stream_id, current_time))
        
        connection.commit()
        return stream_id
    except Exception as e:
        logging.error(f"Erro ao inserir filme {movie_data['stream_display_name']}: {str(e)}")
        connection.rollback()
        raise
    finally:
        cursor.close()

def process_excel_to_database():
    """
    Processa o arquivo Excel e importa os filmes para o banco de dados.
    """
    if not os.path.exists(EXCEL_FILE_PATH):
        logging.error(f"Arquivo {EXCEL_FILE_PATH} não encontrado.")
        return

    # Lista para armazenar todos os IDs inseridos
    inserted_stream_ids = []

    try:
        df = pd.read_excel(EXCEL_FILE_PATH)
        with DatabaseConnection() as connection:
            cursor = connection.cursor(dictionary=True)
            
            # Verificar se o bouquet FILMES existe, se não, criar
            bouquet_id = get_or_create_movies_bouquet(cursor, connection)
            
            for _, row in df.iterrows():
                try:
                    tmdb_id = row['tmdb']
                    link = row['link']
                    
                    if check_if_movie_exists(cursor, tmdb_id):
                        logging.info(f"Filme com TMDB ID {tmdb_id} já existe no banco de dados. Pulando...")
                        continue
                        
                    movie_details = get_movie_details(tmdb_id)
                    
                    if not movie_details:
                        logging.warning(f"Não foi possível obter detalhes do filme {tmdb_id}. Pulando...")
                        continue

                    director, cast = get_movie_credits(tmdb_id)
                    genre_names = [genre['name'] for genre in movie_details.get('genres', [])]
                    category_ids = get_category_ids(cursor, genre_names)
                    
                    runtime_minutes = movie_details.get('runtime', 0)
                    hours = runtime_minutes // 60
                    minutes = runtime_minutes % 60
                    duration = f"{hours:02d}:{minutes:02d}:00"

                    movie_properties = {
                        "kinopoisk_url": f"https://www.themoviedb.org/movie/{tmdb_id}",
                        "tmdb_id": str(tmdb_id),
                        "name": movie_details['title'],
                        "o_name": movie_details.get('original_title', ''),
                        "cover_big": f"https://image.tmdb.org/t/p/w600_and_h900_bestv2{movie_details.get('poster_path', '')}",
                        "movie_image": f"https://image.tmdb.org/t/p/w600_and_h900_bestv2{movie_details.get('poster_path', '')}",
                        "release_date": movie_details.get('release_date', ''),
                        "episode_run_time": str(runtime_minutes),
                        "youtube_trailer": "",
                        "director": director,
                        "actors": cast,
                        "cast": cast,
                        "description": movie_details.get('overview', ''),
                        "plot": movie_details.get('overview', ''),
                        "age": "",
                        "mpaa_rating": "",
                        "rating_count_kinopoisk": 0,
                        "country": ", ".join([country['name'] for country in movie_details.get('production_countries', [])]),
                        "genre": ", ".join(genre_names),
                        "backdrop_path": [f"https://image.tmdb.org/t/p/w1280{movie_details.get('backdrop_path', '')}"],
                        "duration_secs": runtime_minutes * 60,
                        "duration": duration,
                        "video": [],
                        "audio": [],
                        "bitrate": 0,
                        "rating": str(movie_details.get('vote_average', 0))
                    }

                    formatted_link = link.replace('/', '\/')

                    movie_data = {
                        "category_id": f"[{','.join(category_ids)}]",
                        "stream_display_name": movie_details['title'],
                        "stream_source": json.dumps([formatted_link]),
                        "movie_properties": json.dumps(movie_properties),
                        "year": int(movie_details.get('release_date', '0000')[:4]),
                        "rating": movie_details.get('vote_average', 0),
                        "added": int(datetime.now().timestamp()),
                        "updated": '0000-00-00 00:00:00',
                        "tmdb_id": tmdb_id
                    }

                    stream_id = insert_into_database(connection, movie_data)
                    
                    # Adicionar ID à lista de IDs inseridos
                    inserted_stream_ids.append(stream_id)
                    
                    # Atualizar o bouquet com todos os IDs inseridos
                    update_bouquet_with_ids(cursor, bouquet_id, inserted_stream_ids)
                    connection.commit()
                    
                    logging.info(f"Filme {movie_data['stream_display_name']} (ID: {stream_id}) inserido com sucesso e adicionado ao bouquet {bouquet_id}.")
                
                except Exception as e:
                    logging.error(f"Erro ao processar linha do Excel: {str(e)}")
                    connection.rollback()
                    continue

            cursor.close()
            
    except Exception as e:
        logging.error(f"Erro ao processar arquivo Excel: {str(e)}")
    
    logging.info(f"Processamento do arquivo filmes.xlsx concluído. Total de filmes inseridos: {len(inserted_stream_ids)}")
    logging.info(f"IDs inseridos: {inserted_stream_ids}")

if __name__ == "__main__":
    try:
        process_excel_to_database()
    except Exception as e:
        logging.error(f"Erro fatal durante a execução do script: {str(e)}")