import os
import aiohttp
import asyncio
from tqdm import tqdm
import re
from datetime import datetime
from pyrofork import Client
from pyrofork.errors import FloodWait
import logging
from typing import Optional, Dict, List, Tuple
from dataclasses import dataclass
from pathlib import Path
from logging.handlers import RotatingFileHandler

import json
from contextlib import asynccontextmanager

@dataclass
class MovieInfo:
    """Classe para armazenar informações do filme"""
    tmdb_id: int
    title: str
    link: str
    
    def __str__(self) -> str:
        return f"{self.tmdb_id}"

@dataclass
class Config:
    """Configuração centralizada usando dataclass"""
    api_id: str
    api_hash: str
    bot_token: str
    chat_id: str
    download_dir: Path
    json_file: Path
    log_file: Path
    max_retries: int
    chunk_size: int
    concurrent_downloads: int
    upload_timeout: int
    workers: int

    @classmethod
    def from_json(cls, file_path: str) -> 'Config':
        """Carrega configuração de arquivo JSON"""
        with open(file_path) as f:
            config = json.load(f)
            return cls(
                api_id=config['api_id'],
                api_hash=config['api_hash'],
                bot_token=config['bot_token'],
                chat_id=config['chat_id'],
                download_dir=Path(config['download_dir']),
                json_file=Path(config['json_file']),
                log_file=Path(config['log_file']),
                max_retries=config['max_retries'],
                chunk_size=config['chunk_size'],
                concurrent_downloads=config['concurrent_downloads'],
                upload_timeout=config['upload_timeout'],
                workers=config['workers']
            )

class DownloadManager:
    """Gerencia o estado dos downloads e uploads"""
    def __init__(self):
        self.active_downloads: Dict[str, asyncio.Task] = {}
        self.failed_downloads: List[MovieInfo] = []
        self.completed_downloads: List[MovieInfo] = []

    async def add_download(self, movie_info: MovieInfo, task: asyncio.Task):
        self.active_downloads[str(movie_info)] = task
        try:
            await task
            self.completed_downloads.append(movie_info)
        except Exception as e:
            self.failed_downloads.append(movie_info)
        finally:
            del self.active_downloads[str(movie_info)]

    def get_stats(self) -> Dict:
        return {
            'active': len(self.active_downloads),
            'completed': len(self.completed_downloads),
            'failed': len(self.failed_downloads)
        }

class TelegramDownloader:
    def __init__(self, config: Config):
        self.config = config
        self.config.download_dir.mkdir(exist_ok=True)
        self.logger = self._setup_logging()
        
        self.app = Client(
            "file_uploader",
            api_id=config.api_id,
            api_hash=config.api_hash,
            bot_token=config.bot_token,
            sleep_threshold=60
        )
        
        self.session: Optional[aiohttp.ClientSession] = None
        self.download_semaphore = asyncio.Semaphore(config.concurrent_downloads)
        self.download_manager = DownloadManager()

    def _setup_logging(self) -> logging.Logger:
        """Configuração de logging com rotação de arquivos"""
        logger = logging.getLogger(__name__)
        logger.setLevel(logging.INFO)
        
        formatter = logging.Formatter(
            '%(asctime)s - %(name)s - %(levelname)s - %(message)s'
        )
        
        file_handler = RotatingFileHandler(
            'downloader.log',
            maxBytes=10*1024*1024,
            backupCount=5
        )
        file_handler.setFormatter(formatter)
        
        console_handler = logging.StreamHandler()
        console_handler.setFormatter(formatter)
        
        logger.addHandler(file_handler)
        logger.addHandler(console_handler)
        
        return logger

    @asynccontextmanager
    async def session_context(self):
        """Context manager para gerenciar a sessão HTTP"""
        timeout = aiohttp.ClientTimeout(total=self.config.upload_timeout)
        async with aiohttp.ClientSession(
            timeout=timeout,
            connector=aiohttp.TCPConnector(
                limit=self.config.concurrent_downloads,
                force_close=True,
                enable_cleanup_closed=True,
                ttl_dns_cache=300
            )
        ) as session:
            self.session = session
            try:
                yield
            finally:
                self.session = None

    async def initialize(self):
        """Inicialização com tratamento de erros"""
        try:
            await self.app.start()
            self.logger.info("Cliente Telegram inicializado com sucesso")
        except Exception as e:
            self.logger.error(f"Erro na inicialização: {e}")
            raise

    async def cleanup(self):
        """Limpeza de recursos"""
        try:
            await self.app.stop()
            self.logger.info("Recursos limpos com sucesso")
        except Exception as e:
            self.logger.error(f"Erro na limpeza: {e}")
            raise

    async def download_file(self, url: str, file_path: Path) -> bool:
        """Download com gerenciamento de erros e progresso"""
        headers = {
            'User-Agent': 'Mozilla/5.0',
            'Connection': 'keep-alive',
            'Accept-Encoding': 'gzip, deflate'
        }
        temp_path = file_path.with_suffix('.temp')
        
        async with self.download_semaphore:
            try:
                async with self.session.get(url, headers=headers) as response:
                    response.raise_for_status()
                    
                    total_size = int(response.headers.get('content-length', 0))
                    
                    with open(temp_path, 'wb') as f, tqdm(
                        desc=f"Downloading {file_path.name}",
                        total=total_size,
                        unit='iB',
                        unit_scale=True,
                        unit_divisor=1024
                    ) as progress_bar:
                        async for chunk in response.content.iter_chunked(
                            self.config.chunk_size
                        ):
                            if chunk:
                                f.write(chunk)
                                progress_bar.update(len(chunk))
                    
                    temp_path.replace(file_path)
                    return True
                    
            except Exception as e:
                self.logger.error(f"Erro no download: {e}")
                if temp_path.exists():
                    temp_path.unlink()
                return False

    async def upload_file(self, file_path: Path, file_name: str) -> bool:
        """Upload com retry para FloodWait"""
        file_size = file_path.stat().st_size
        
        with tqdm(
            desc=f"Uploading {file_name}",
            total=file_size,
            unit='iB',
            unit_scale=True,
            unit_divisor=1024
        ) as progress_bar:
            try:
                async def progress(current, total):
                    progress_bar.n = current
                    progress_bar.refresh()

                await self.app.send_document(
                    chat_id=self.config.chat_id,
                    document=str(file_path),
                    file_name=file_name,
                    progress=progress,
                    force_document=True
                )
                
                self.logger.info(f"Arquivo {file_name} enviado com sucesso")
                return True
            except FloodWait as e:
                self.logger.warning(f"Limite de taxa atingido. Aguardando {e.value} segundos...")
                await asyncio.sleep(e.value)
                return await self.upload_file(file_path, file_name)
            except Exception as e:
                self.logger.error(f"Erro no upload: {e}")
                return False

    def log_download(self, movie_info: MovieInfo):
        """Log com timestamp ISO e informações do filme"""
        with open(self.config.log_file, 'a', encoding='utf-8') as log:
            timestamp = datetime.now().isoformat()
            log.write(f"{timestamp} - {movie_info}\n")

    def is_downloaded(self, movie_info: MovieInfo) -> bool:
        """Verifica se filme já foi baixado baseado no TMDB ID"""
        if self.config.log_file.exists():
            with open(self.config.log_file, 'r', encoding='utf-8') as log:
                return any(str(movie_info) in line for line in log)
        return False

    async def process_url(self, movie_info: MovieInfo) -> bool:
        """Processamento de URL com informações do filme"""
        # Usa apenas o tmdb_id como nome do arquivo
        file_name = f"{movie_info.tmdb_id}.mp4"
        file_path = self.config.download_dir / file_name
        
        try:
            self.logger.info(f"Iniciando download do filme {movie_info.title} (ID: {movie_info.tmdb_id})")
            if not await self.download_file(movie_info.link, file_path):
                return False
            
            self.logger.info(f"Iniciando upload do filme {movie_info.title} (ID: {movie_info.tmdb_id})")
            success = await self.upload_file(file_path, file_name)
            
            if success:
                self.log_download(movie_info)
            
            return success
            
        except Exception as e:
            self.logger.error(f"Erro processando filme {movie_info.title} (ID: {movie_info.tmdb_id}): {e}")
            return False
        finally:
            if file_path.exists():
                file_path.unlink()
                self.logger.info(f"Arquivo {file_name} removido do disco")

    async def process_json(self):
        """Processamento de JSON com informações de filmes"""
        if not self.config.json_file.is_file():
            raise FileNotFoundError(f'Arquivo {self.config.json_file} não encontrado')

        async with self.session_context():
            with open(self.config.json_file, 'r', encoding='utf-8') as file:
                movie_list = json.load(file)
                
            if not isinstance(movie_list, list):
                # Se o JSON não for uma lista, tenta tratar como um objeto único
                movie_list = [movie_list]
                
            for movie_data in movie_list:
                movie_info = MovieInfo(
                    tmdb_id=movie_data['tmdbid'],
                    title=movie_data['title'],
                    link=movie_data['link']
                )
                
                if self.is_downloaded(movie_info):
                    self.logger.info(f'Filme já processado: {movie_info.title} (ID: {movie_info.tmdb_id})')
                    continue
                
                task = asyncio.create_task(
                    self.process_url(movie_info)
                )
                await self.download_manager.add_download(movie_info, task)

            stats = self.download_manager.get_stats()
            self.logger.info(f"Estatísticas finais: {stats}")

async def main():
    """Função principal com tratamento de erros"""
    try:
        config = Config.from_json('config.json')
        downloader = TelegramDownloader(config)
        
        await downloader.initialize()
        try:
            await downloader.process_json()
        finally:
            await downloader.cleanup()
            
    except Exception as e:
        logging.error(f"Erro fatal: {e}", exc_info=True)
        raise

if __name__ == '__main__':
    asyncio.run(main())