import os
import json
import mysql.connector
import requests
import pandas as pd
from datetime import datetime
from dotenv import load_dotenv
from tqdm import tqdm
from functools import lru_cache
from mysql.connector import pooling
from typing import Dict, List, Optional, Set, Tuple, Any
from pathlib import Path
import logging
from concurrent.futures import ThreadPoolExecutor, as_completed
import time
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s',
    handlers=[
        logging.FileHandler('series_processor.log'),
        logging.StreamHandler()
    ]
)
logger = logging.getLogger(__name__)

# Load environment variables
load_dotenv()

# Configuration
class Config:
    TMDB_API_KEY = os.getenv('TMDB_API_KEY')
    if not TMDB_API_KEY:
        raise ValueError("TMDB_API_KEY not found in environment variables")
    
    TMDB_BASE_URL = 'https://api.themoviedb.org/3'
    EXCEL_FILE_PATH = Path('excel/series.xlsx')
    DB_CONFIG_PATH = Path('db/db_config.json')
    BATCH_SIZE = 50
    DEFAULT_CATEGORY_NAME = 'outros'
    MAX_WORKERS = 5
    REQUEST_TIMEOUT = 10
    MAX_RETRIES = 3

# Configure requests session with retry strategy
def create_requests_session():
    session = requests.Session()
    retry_strategy = Retry(
        total=Config.MAX_RETRIES,
        backoff_factor=1,
        status_forcelist=[429, 500, 502, 503, 504]
    )
    adapter = HTTPAdapter(max_retries=retry_strategy)
    session.mount("http://", adapter)
    session.mount("https://", adapter)
    return session

class DatabaseConnection:
    def __init__(self):
        self.pool = self._create_connection_pool()

    def _create_connection_pool(self):
        """Create and return a connection pool"""
        try:
            with open(Config.DB_CONFIG_PATH, 'r') as file:
                db_config = json.load(file)
                db_config['pool_name'] = 'series_pool'
                db_config['pool_size'] = Config.MAX_WORKERS
            return mysql.connector.pooling.MySQLConnectionPool(**db_config)
        except Exception as e:
            logger.error(f"Failed to create connection pool: {e}")
            raise

    @property
    def connection(self):
        return self.pool.get_connection()

class TMDBClient:
    def __init__(self):
        self.session = create_requests_session()
        self._image_base_url = "https://image.tmdb.org/t/p"

    @lru_cache(maxsize=1000)
    def get_data(self, endpoint: str, language: str = None, append_to_response: str = None) -> Optional[dict]:
        """Fetch data from TMDB API with caching"""
        url = f"{Config.TMDB_BASE_URL}/{endpoint}"
        params = {'api_key': Config.TMDB_API_KEY}
        
        if language:
            params['language'] = language
        if append_to_response:
            params['append_to_response'] = append_to_response

        try:
            response = self.session.get(url, params=params, timeout=Config.REQUEST_TIMEOUT)
            response.raise_for_status()
            return response.json()
        except requests.RequestException as e:
            logger.error(f"TMDB API request failed for {endpoint}: {e}")
            return None

    def get_poster_url(self, path: str, size: str = 'w600_and_h900_bestv2') -> str:
        """Generate poster URL with given size"""
        if not path:
            return ''
        return f"{self._image_base_url}/{size}{path}"

    def get_backdrop_url(self, path: str, size: str = 'w1280') -> str:
        """Generate backdrop URL with given size"""
        if not path:
            return ''
        return f"{self._image_base_url}/{size}{path}"

class CategoryManager:
    def __init__(self, db: DatabaseConnection):
        self.db = db
        self._category_mapping = None

    @property
    def category_mapping(self) -> Dict[str, int]:
        """Lazy load and cache category mapping"""
        if self._category_mapping is None:
            self._category_mapping = self._load_categories()
        return self._category_mapping

    def _load_categories(self) -> Dict[str, int]:
        """Load categories from database"""
        with self.db.connection as conn:
            cursor = conn.cursor(dictionary=True)
            cursor.execute("""
                SELECT id, category_name 
                FROM streams_categories 
                WHERE category_type = 'series' AND parent_id = 0
            """)
            
            return {
                category['category_name'].lower().strip(): category['id']
                for category in cursor.fetchall()
            }

    def get_default_category_id(self) -> int:
        """Get or create the default category ID"""
        with self.db.connection as conn:
            cursor = conn.cursor(dictionary=True)
            
            cursor.execute("""
                SELECT id FROM streams_categories 
                WHERE LOWER(category_name) = %s AND category_type = 'series' AND parent_id = 0
                LIMIT 1
            """, (Config.DEFAULT_CATEGORY_NAME,))
            result = cursor.fetchone()
            
            if result:
                return result['id']
            
            cursor.execute("""
                INSERT INTO streams_categories (category_name, category_type, parent_id)
                VALUES (%s, 'series', 0)
            """, (Config.DEFAULT_CATEGORY_NAME.capitalize(),))
            conn.commit()
            return cursor.lastrowid

class SeriesProcessor:
    def __init__(self, db: DatabaseConnection, tmdb: TMDBClient, category_mgr: CategoryManager):
        self.db = db
        self.tmdb = tmdb
        self.category_mgr = category_mgr
        # Asian country codes and languages commonly associated with doramas
        self.asian_country_codes = {'KR', 'JP', 'CN', 'TW', 'TH', 'VN', 'ID', 'MY', 'PH'}
        self.asian_languages = {'ko', 'ja', 'zh', 'th', 'vi', 'id', 'ms', 'tl'}
        # Common terms that indicate anime content
        self.anime_indicators = {
            'anime', 'animation', 'animated series', 'アニメ',
            'manga adaptation', 'light novel adaptation',
            'shounen', 'shoujo', 'seinen', 'josei',
            'mecha', 'isekai', 'slice of life'
        }
        # Anime-specific studios and producers
        self.anime_studios = {
            'toei animation', 'madhouse', 'bones', 'shaft', 'a-1 pictures',
            'production i.g', 'kyoto animation', 'ufotable', 'wit studio',
            'mappa', 'trigger', 'sunrise', 'gainax', 'pierrot', 'j.c.staff'
        }

    def is_anime(self, series_data: dict) -> bool:
        """
        Determine if a series is an anime based on various indicators
        """
        # Check genres
        genres = {genre['name'].lower() for genre in series_data.get('genres', [])}
        if 'animation' in genres:
            # Additional checks for animation type
            keywords = set()
            
            # Check production companies
            for company in series_data.get('production_companies', []):
                company_name = company['name'].lower()
                keywords.add(company_name)
                # Check for anime studios
                if company_name in self.anime_studios:
                    return True
            
            # Check keywords and networks
            for network in series_data.get('networks', []):
                network_name = network['name'].lower()
                keywords.add(network_name)
                # Common anime networks/platforms
                if any(name in network_name for name in ['crunchyroll', 'funimation', 'aniplex']):
                    return True

            # Check for anime-specific keywords
            if any(indicator in keywords for indicator in self.anime_indicators):
                return True

            # Check production country
            production_countries = series_data.get('production_countries', [])
            if len(production_countries) == 1 and production_countries[0].get('iso_3166_1') == 'JP':
                origin_language = series_data.get('original_language', '')
                if origin_language == 'ja':
                    return True

        return False

    def is_dorama(self, series_data: dict) -> bool:
        """
        Determine if a series is a dorama based on various indicators
        """
        # Skip if it's an anime
        if self.is_anime(series_data):
            return False

        # Check origin country
        origin_country = series_data.get('origin_country', [])
        if any(country in self.asian_country_codes for country in origin_country):
            # Check original language
            original_language = series_data.get('original_language', '')
            if original_language in self.asian_languages:
                # Additional verification to exclude animated content
                genres = {genre['name'].lower() for genre in series_data.get('genres', [])}
                if 'animation' not in genres:
                    return True

        # Check production companies and networks for Asian studios
        companies = series_data.get('production_companies', [])
        networks = series_data.get('networks', [])
        
        for entity in companies + networks:
            origin_country = entity.get('origin_country', '')
            if origin_country in self.asian_country_codes:
                return True

        return False

    def get_provider_category(self, series_data: dict) -> str:
        """Determina a categoria correta com base nas palavras-chave, provedores e outras fontes."""
        category_mapping = self.category_mgr.category_mapping
        default_category_id = self.category_mgr.get_default_category_id()

        # Obter palavras-chave da API (se disponíveis)
        keywords_data = self.tmdb.get_data(f"tv/{series_data['id']}/keywords")
        keywords = {kw['name'].lower() for kw in keywords_data.get('results', [])} if keywords_data else set()

        # Se palavras-chave indicarem anime, priorizar Crunchyroll/Funimation
        if any(keyword in self.anime_indicators for keyword in keywords):
            anime_category_variations = {
                'crunchyroll/funimation', 'crunchyroll', 'funimation', 'anime', 'animes',
                'manga', 'otaku', 'shonen', 'seinen', 'animeclassico', 'animejapanese'
            }
            for variation in anime_category_variations:
                if variation in category_mapping:
                    return f'[{category_mapping[variation]}]'
        
        # Se for dorama e **não for** anime, aplicar a categoria correspondente
        if self.is_dorama(series_data):
            dorama_variations = {
                'dorama', 'doramas', 'drama asiático', 'dramas asiáticos', 'drama coreano',
                'drama chinês', 'kdrama', 'jdrama', 'cdrama', 'taiwan drama'
            }
            for variation in dorama_variations:
                if variation in category_mapping:
                    return f'[{category_mapping[variation]}]'

        # Se não for anime nem dorama, tenta identificar por provedores
        provider_names = set()
        matched_categories = {}

        # Buscar provedores de streaming globalmente
        providers_data = self.tmdb.get_data(f"tv/{series_data['id']}/watch/providers")
        if providers_data:
            # Iterar sobre todos os provedores, independentemente da região
            for country_data in providers_data.get('results', {}).values():
                for provider_type in ['flatrate', 'rent', 'buy']:
                    for provider in country_data.get(provider_type, []):
                        provider_names.add(provider.get('provider_name', '').lower().strip())

        # Adicionar redes de TV e estúdios como possíveis categorias
        for source in ['networks', 'production_companies']:
            for item in series_data.get(source, []):
                provider_names.add(item.get('name', '').lower().strip())

        # Associar aos provedores existentes no banco de dados
        for provider in provider_names:
            for category_name, category_id in category_mapping.items():
                if provider == category_name or category_name in provider or provider in category_name:
                    # Adiciona as categorias ao dicionário, contabilizando as ocorrências
                    if category_id in matched_categories:
                        matched_categories[category_id].append(provider)
                    else:
                        matched_categories[category_id] = [provider]
                    break

        # Organizar as categorias de acordo com a quantidade de provedores (menor número de ocorrências primeiro)
        sorted_categories = sorted(matched_categories.items(), key=lambda x: len(x[1]))

        # Seleciona as categorias ordenadas e retorna a categoria primária (com menos provedores)
        if sorted_categories:
            primary_category_id = sorted_categories[0][0]
        else:
            # Se nenhuma categoria foi encontrada, atribuir categoria padrão
            primary_category_id = default_category_id

        return f'[{primary_category_id}]'



    def process_series_details(self, tmdb_id: int) -> Optional[dict]:
        """Process and prepare series details for insertion"""
        series_data = self.tmdb.get_data(f'tv/{tmdb_id}', language='pt-BR', append_to_response='similar')
        if not series_data:
            return None

        # Process basic info
        series_name = series_data.get('name') or series_data.get('original_name', 'Título Desconhecido')
        first_air_date = series_data.get('first_air_date', '')
        
        # Process seasons
        seasons = []
        for season in series_data.get('seasons', []):
            season_data = self.tmdb.get_data(
                f'tv/{tmdb_id}/season/{season["season_number"]}',
                language='pt-BR'
            )
            
            if season_data:
                season_name = season_data.get('name') or f"Temporada {season['season_number']}"
                seasons.append({
                    'air_date': season.get('air_date', ''),
                    'episode_count': season.get('episode_count', 0),
                    'id': season.get('id', 0),
                    'name': season_name,
                    'overview': season_data.get('overview', ''),
                    'season_number': season.get('season_number', 0),
                    'vote_average': season.get('vote_average', 0),
                    'cover': self.tmdb.get_poster_url(season.get('poster_path')),
                    'cover_big': self.tmdb.get_poster_url(season.get('poster_path'))
                })

        return {
            'title': series_name,
            'category_id': self.get_provider_category(series_data),
            'cover': self.tmdb.get_poster_url(series_data.get('poster_path')),
            'cover_big': self.tmdb.get_poster_url(series_data.get('poster_path')),
            'genre': ', '.join(genre['name'] for genre in series_data.get('genres', [])),
            'plot': series_data.get('overview', ''),
            'cast': '',  # Could be enhanced with credits API
            'rating': series_data.get('vote_average', 0),
            'director': None,
            'release_date': first_air_date,
            'last_modified': int(datetime.now().timestamp()),
            'tmdb_id': tmdb_id,
            'seasons': json.dumps(seasons),
            'episode_run_time': series_data.get('episode_run_time', [0])[0] if series_data.get('episode_run_time') else 0,
            'backdrop_path': json.dumps([self.tmdb.get_backdrop_url(series_data.get('backdrop_path', ''))]),
            'youtube_trailer': None,  # Could be enhanced with videos API
            'tmdb_language': series_data.get('original_language', ''),
            'year': int(first_air_date.split('-')[0]) if first_air_date else 0,
            'plex_uuid': "''",
            'similar': json.dumps([item['id'] for item in series_data.get('similar', {}).get('results', [])])
        }

class SeriesBatchProcessor:
    def __init__(self, db: DatabaseConnection, series_processor: SeriesProcessor):
        self.db = db
        self.series_processor = series_processor
        self.stats = {'total': 0, 'processed': 0, 'inserted': 0, 'errors': 0}
        self.inserted_series_ids = []

    def batch_insert_series(self, series_batch: List[dict]) -> List[int]:
        """Insert multiple series in a single transaction"""
        if not series_batch:
            return []
        with self.db.connection as conn:
            cursor = conn.cursor()
            
            inserted_ids = []
            for series_data in series_batch:
                query = """
                INSERT INTO streams_series (
                    title, category_id, cover, cover_big, genre, plot, cast, rating,
                    director, release_date, last_modified, tmdb_id, seasons,
                    episode_run_time, backdrop_path, youtube_trailer, tmdb_language,
                    year, plex_uuid, similar
                ) VALUES (
                    %(title)s, %(category_id)s, %(cover)s, %(cover_big)s, %(genre)s,
                    %(plot)s, %(cast)s, %(rating)s, %(director)s, %(release_date)s,
                    %(last_modified)s, %(tmdb_id)s, %(seasons)s, %(episode_run_time)s,
                    %(backdrop_path)s, %(youtube_trailer)s, %(tmdb_language)s,
                    %(year)s, %(plex_uuid)s, %(similar)s
                )
                """
                cursor.execute(query, series_data)
                inserted_ids.append(cursor.lastrowid)
            
            conn.commit()
            return inserted_ids

    def update_bouquet(self, bouquet_name: str = 'SERIES'):
        """Update bouquet with new series IDs"""
        if not self.inserted_series_ids:
            return
        
        with self.db.connection as conn:
            cursor = conn.cursor(dictionary=True)
            
            cursor.execute("SELECT bouquet_series FROM bouquets WHERE bouquet_name = %s", (bouquet_name,))
            result = cursor.fetchone()
            
            existing_ids = json.loads(result['bouquet_series']) if result and result['bouquet_series'] else []
            all_series_ids = list(dict.fromkeys(existing_ids + self.inserted_series_ids))
            
            if result:
                cursor.execute(
                    "UPDATE bouquets SET bouquet_series = %s WHERE bouquet_name = %s",
                    (json.dumps(all_series_ids), bouquet_name)
                )
            else:
                cursor.execute(
                    """INSERT INTO bouquets (bouquet_name, bouquet_series, bouquet_order)
                    VALUES (%s, %s, 2)""",
                    (bouquet_name, json.dumps(all_series_ids))
                )
            
            conn.commit()

    def process_tmdb_id(self, tmdb_id: int) -> Optional[int]:
        """Process a single TMDB ID"""
        try:
            # Check if series exists
            with self.db.connection as conn:
                cursor = conn.cursor()
                cursor.execute("SELECT id FROM streams_series WHERE tmdb_id = %s", (tmdb_id,))
                if cursor.fetchone():
                    return None

            # Process series details
            series_data = self.series_processor.process_series_details(tmdb_id)
            if series_data:
                inserted_ids = self.batch_insert_series([series_data])
                return inserted_ids[0] if inserted_ids else None

        except Exception as e:
            logger.error(f"Error processing series {tmdb_id}: {e}")
            self.stats['errors'] += 1
            return None

    def process_all(self, tmdb_ids: List[int]):
        """Process all TMDB IDs with progress tracking"""
        self.stats['total'] = len(tmdb_ids)
        
        with ThreadPoolExecutor(max_workers=Config.MAX_WORKERS) as executor:
            future_to_id = {executor.submit(self.process_tmdb_id, tmdb_id): tmdb_id 
                           for tmdb_id in tmdb_ids}
            with tqdm(total=len(tmdb_ids), desc="Processando séries") as pbar:
                for future in as_completed(future_to_id):
                    tmdb_id = future_to_id[future]
                    try:
                        series_id = future.result()
                        if series_id:
                            self.inserted_series_ids.append(series_id)
                            self.stats['inserted'] += 1
                    except Exception as e:
                        logger.error(f"Error processing TMDB ID {tmdb_id}: {e}")
                        self.stats['errors'] += 1
                    finally:
                        self.stats['processed'] += 1
                        pbar.update(1)

    def print_summary(self):
        """Print processing summary"""
        logger.info("\nResumo do processamento:")
        logger.info(f"Total de séries processadas: {self.stats['processed']}")
        logger.info(f"Séries inseridas com sucesso: {self.stats['inserted']}")
        logger.info(f"Erros encontrados: {self.stats['errors']}")

class DataLoader:
    @staticmethod
    def load_tmdb_ids(file_path: Path) -> List[int]:
        """Load TMDB IDs from Excel file"""
        try:
            df = pd.read_excel(file_path)
            return df['tmdb'].dropna().astype(int).unique().tolist()
        except Exception as e:
            logger.error(f"Error loading TMDB IDs from Excel: {e}")
            raise

def main():
    """Main execution flow"""
    try:
        # Initialize components
        db = DatabaseConnection()
        tmdb = TMDBClient()
        category_mgr = CategoryManager(db)
        series_processor = SeriesProcessor(db, tmdb, category_mgr)
        batch_processor = SeriesBatchProcessor(db, series_processor)

        # Load TMDB IDs
        logger.info("Loading TMDB IDs from Excel...")
        tmdb_ids = DataLoader.load_tmdb_ids(Config.EXCEL_FILE_PATH)
        logger.info(f"Loaded {len(tmdb_ids)} TMDB IDs")

        # Process all series
        batch_processor.process_all(tmdb_ids)

        # Update bouquet
        if batch_processor.inserted_series_ids:
            logger.info("Updating bouquet...")
            batch_processor.update_bouquet()

        # Print summary
        batch_processor.print_summary()

    except Exception as e:
        logger.error(f"Fatal error: {e}")
        raise

if __name__ == "__main__":
    main()