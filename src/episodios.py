import os
import json
import logging
import mysql.connector
import requests
import pandas as pd
from dotenv import load_dotenv
from tqdm import tqdm
from typing import Dict, List, Optional, Tuple
from concurrent.futures import ThreadPoolExecutor, as_completed
from mysql.connector import pooling
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry

# Logging: arquivo + console
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s',
    handlers=[
        logging.FileHandler('episodios_processor.log', encoding='utf-8'),
        logging.StreamHandler()
    ]
)
logger = logging.getLogger(__name__)

load_dotenv()

tmdb_API_KEY = os.getenv('TMDB_API_KEY')
if not tmdb_API_KEY:
    raise ValueError("TMDB_API_KEY not found in environment variables")
tmdb_SERIES_EPISODE_URL = 'https://api.themoviedb.org/3/tv/{}/season/{}/episode/{}'
DB_CONFIG_PATH = './db/db_config.json'
EXCEL_FILE_PATH = 'excel/series.xlsx'
BATCH_SIZE = 200            # inserts por lote no MySQL
TMDB_WORKERS = 20           # chamadas TMDB paralelas (~40 req/s cabe no rate limit)
DB_POOL_SIZE = 8
REQUEST_TIMEOUT = 10

# Pool MySQL
with open(DB_CONFIG_PATH, 'r') as file:
    db_config = json.load(file)
    db_config['pool_name'] = 'mypool'
    db_config['pool_size'] = DB_POOL_SIZE

connection_pool = mysql.connector.pooling.MySQLConnectionPool(**db_config)

# HTTP session com keep-alive + retry
def _make_session():
    s = requests.Session()
    retry = Retry(total=3, backoff_factor=0.5,
                  status_forcelist=[429, 500, 502, 503, 504],
                  allowed_methods=['GET'])
    adapter = HTTPAdapter(pool_connections=TMDB_WORKERS,
                          pool_maxsize=TMDB_WORKERS,
                          max_retries=retry)
    s.mount('http://', adapter)
    s.mount('https://', adapter)
    return s

http_session = _make_session()


def get_episode_details(tmdb_id: int, season_number: int, episode_number: int) -> Optional[dict]:
    """Fetch episode details from TMDB API (sem lru_cache: memoria pequena e cache nao ajuda em run unico)."""
    url = tmdb_SERIES_EPISODE_URL.format(tmdb_id, season_number, episode_number)
    try:
        response = http_session.get(url,
                                    params={'api_key': tmdb_API_KEY, 'language': 'pt-BR'},
                                    timeout=REQUEST_TIMEOUT)
        response.raise_for_status()
        return response.json()
    except requests.RequestException as e:
        logger.warning(f"TMDB falhou tv/{tmdb_id}/season/{season_number}/episode/{episode_number}: {e}")
        return None


def preload_series_map(tmdb_ids: List[int]) -> Dict[int, dict]:
    """Retorna {tmdb_id: {'id','title','seasons'}} em UMA query."""
    if not tmdb_ids:
        return {}
    connection = connection_pool.get_connection()
    try:
        cursor = connection.cursor(dictionary=True)
        placeholders = ','.join(['%s'] * len(tmdb_ids))
        cursor.execute(
            f"SELECT id, title, seasons, tmdb_id FROM streams_series WHERE tmdb_id IN ({placeholders})",
            tmdb_ids
        )
        return {row['tmdb_id']: row for row in cursor.fetchall()}
    finally:
        cursor.close()
        connection.close()


def get_existing_episodes(series_ids: List[int]) -> set:
    """Retorna set de (series_id, season, episode) que ja existem."""
    if not series_ids:
        return set()
    connection = connection_pool.get_connection()
    try:
        cursor = connection.cursor()
        placeholders = ','.join(['%s'] * len(series_ids))
        cursor.execute(
            f"SELECT series_id, season_num, episode_num FROM streams_episodes WHERE series_id IN ({placeholders})",
            series_ids
        )
        return {(r[0], r[1], r[2]) for r in cursor.fetchall()}
    finally:
        cursor.close()
        connection.close()


def batch_insert_episodes(episodes_data: List[dict]) -> List[int]:
    """Insert multiple episodes in a single transaction."""
    if not episodes_data:
        return []

    connection = connection_pool.get_connection()
    try:
        cursor = connection.cursor()

        streams_query = """
            INSERT INTO streams (
                type, stream_display_name, stream_source, notes, movie_properties,
                target_container, `order`, gen_timestamps, added, series_no,
                direct_source, tmdb_language, year, rating, updated, tmdb_id,
                direct_proxy
            ) VALUES (
                %(type)s, %(stream_display_name)s, %(stream_source)s, %(notes)s,
                %(movie_properties)s, %(target_container)s, %(order)s,
                %(gen_timestamps)s, %(added)s, %(series_no)s, %(direct_source)s,
                %(tmdb_language)s, %(year)s, %(rating)s, %(updated)s, %(tmdb_id)s,
                %(direct_proxy)s
            )
        """
        cursor.executemany(streams_query, episodes_data)

        first_id = cursor.lastrowid
        stream_ids = list(range(first_id, first_id + len(episodes_data)))

        episodes_series_data = [
            (ed['season_num'], ed['episode_num'], ed['series_id'], sid)
            for ed, sid in zip(episodes_data, stream_ids)
        ]
        cursor.executemany(
            "INSERT INTO streams_episodes (season_num, episode_num, series_id, stream_id) VALUES (%s, %s, %s, %s)",
            episodes_series_data
        )

        current_timestamp = pd.Timestamp.now().strftime('%Y-%m-%d %H:%M:%S')
        streams_servers_data = [(sid, 1, current_timestamp) for sid in stream_ids]
        cursor.executemany(
            "INSERT INTO streams_servers (stream_id, server_id, updated) VALUES (%s, %s, %s)",
            streams_servers_data
        )

        connection.commit()
        return stream_ids
    except Exception:
        connection.rollback()
        raise
    finally:
        cursor.close()
        connection.close()


def prepare_episode_data(row: pd.Series, series_info: dict, episode_details: dict, index: int) -> dict:
    current_timestamp = int(pd.Timestamp.now().timestamp())
    runtime_seconds = episode_details.get('runtime', 0) or 0

    hours = runtime_seconds // 3600
    minutes = (runtime_seconds % 3600) // 60
    seconds = runtime_seconds % 60
    duration_str = f"{hours:02d}:{minutes:02d}:{seconds:02d}"

    air_date = episode_details.get('air_date') or ''
    year = int(air_date[:4]) if air_date[:4].isdigit() else 1970

    return {
        "type": 5,
        "stream_display_name": f"{series_info['title']} - S{row['temporada']:02d}E{row['episodio']:02d} - {episode_details.get('name', 'Sem título')}",
        "stream_source": json.dumps([row['link']]),
        "notes": episode_details.get('overview', ''),
        "movie_properties": json.dumps({
            "release_date": year,
            "plot": episode_details.get('overview', ''),
            "duration_secs": runtime_seconds,
            "duration": duration_str,
            "movie_image": f"https://image.tmdb.org/t/p/w1280{episode_details.get('still_path', '')}",
            "rating": str(episode_details.get('vote_average', 0)),
            "season": str(row['temporada']),
            "tmdb_id": str(episode_details.get('id', 0))
        }),
        "target_container": 'mp4',
        "order": index,
        "gen_timestamps": 1,
        "added": current_timestamp,
        "series_no": row['temporada'],
        "direct_source": 1,
        "tmdb_language": episode_details.get('original_language', 'pt'),
        "year": year,
        "rating": float(episode_details.get('vote_average', 0)),
        "updated": current_timestamp,
        "tmdb_id": episode_details.get('id', 0),
        "season_num": row['temporada'],
        "episode_num": row['episodio'],
        "series_id": series_info['id'],
        "direct_proxy": 1
    }


def process_excel_to_database():
    if not os.path.exists(EXCEL_FILE_PATH):
        logger.error(f"Arquivo {EXCEL_FILE_PATH} não encontrado.")
        return

    df = pd.read_excel(EXCEL_FILE_PATH)
    logger.info(f"Excel carregado: {len(df)} linhas")

    # 1) Pre-carregar series num unico SELECT
    tmdb_ids = df['tmdb'].dropna().astype(int).unique().tolist()
    series_map = preload_series_map(tmdb_ids)
    logger.info(f"Series em streams_series: {len(series_map)}/{len(tmdb_ids)} tmdb_ids do Excel")

    # 2) Pre-carregar episodios existentes em UMA query
    series_ids = [s['id'] for s in series_map.values()]
    existing = get_existing_episodes(series_ids)
    logger.info(f"Episodios ja existentes em streams_episodes: {len(existing)}")

    # 3) Filtrar linhas que precisam de fetch TMDB + insert
    work = []           # list of (row_index, row, series_info)
    series_nao_cadastrada = 0
    total_existentes = 0
    for idx, row in df.iterrows():
        tmdb_id = int(row['tmdb']) if pd.notna(row['tmdb']) else None
        if tmdb_id is None or tmdb_id not in series_map:
            series_nao_cadastrada += 1
            continue
        s = series_map[tmdb_id]
        key = (s['id'], int(row['temporada']), int(row['episodio']))
        if key in existing:
            total_existentes += 1
            continue
        work.append((idx, row, s))

    logger.info(f"A fazer: {len(work)} episodios (skip existentes={total_existentes}, sem serie={series_nao_cadastrada})")

    if not work:
        logger.info("Nada a inserir. Fim.")
        return

    # 4) Fetch TMDB em paralelo
    tmdb_falhas = 0
    fetched = []  # (idx, row, series_info, episode_details)
    with tqdm(total=len(work), desc="TMDB fetch") as pbar:
        with ThreadPoolExecutor(max_workers=TMDB_WORKERS) as ex:
            future_to_item = {
                ex.submit(get_episode_details,
                          int(row['tmdb']), int(row['temporada']), int(row['episodio'])): (idx, row, s)
                for (idx, row, s) in work
            }
            for fut in as_completed(future_to_item):
                idx, row, s = future_to_item[fut]
                details = fut.result()
                if details is None:
                    tmdb_falhas += 1
                else:
                    fetched.append((idx, row, s, details))
                pbar.update(1)

    logger.info(f"TMDB concluido: {len(fetched)} OK, {tmdb_falhas} falhas")

    # 5) Montar batches e inserir
    total_inseridos = 0
    lote_erros = 0
    batch = []
    with tqdm(total=len(fetched), desc="DB insert") as pbar:
        for idx, row, s, details in fetched:
            batch.append(prepare_episode_data(row, s, details, int(idx)))
            if len(batch) >= BATCH_SIZE:
                try:
                    ids = batch_insert_episodes(batch)
                    total_inseridos += len(ids)
                except Exception as e:
                    logger.error(f"Erro lote de {len(batch)}: {e}")
                    lote_erros += 1
                pbar.update(len(batch))
                batch = []
        if batch:
            try:
                ids = batch_insert_episodes(batch)
                total_inseridos += len(ids)
            except Exception as e:
                logger.error(f"Erro ultimo lote de {len(batch)}: {e}")
                lote_erros += 1
            pbar.update(len(batch))

    logger.info("=" * 60)
    logger.info("Estatisticas do processamento:")
    logger.info(f"  Linhas no Excel:                {len(df)}")
    logger.info(f"  Ja existiam (skip):             {total_existentes}")
    logger.info(f"  Sem serie em streams_series:    {series_nao_cadastrada}")
    logger.info(f"  Fetches TMDB OK:                {len(fetched)}")
    logger.info(f"  Falhas TMDB:                    {tmdb_falhas}")
    logger.info(f"  Inseridos com sucesso:          {total_inseridos}")
    logger.info(f"  Erros de insercao em lote:      {lote_erros}")


if __name__ == "__main__":
    process_excel_to_database()
