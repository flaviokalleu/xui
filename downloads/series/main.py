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
import time

@dataclass
class EpisodeInfo:
    """Classe para armazenar informações do episódio"""
    tmdb_id: str
    temporada: int
    episodio: int

    def __str__(self) -> str:
        return f"{self.tmdb_id}_S{self.temporada:02d}E{self.episodio:02d}"


@dataclass
class Config:
    """Configuração centralizada usando dataclass"""
    api_id: str
    api_hash: str
    bot_token: str
    chat_id: str
    diretorio_download: Path
    arquivo_m3u: Path
    arquivo_log: Path
    max_tentativas: int
    tamanho_chunk: int
    downloads_simultaneos: int
    tempo_limite_upload: int
    workers: int
    velocidade_minima: int = 500 * 1024  # 500 KB/s em bytes

    @classmethod
    def from_json(cls, caminho_arquivo: str) -> 'Config':
        """Carrega configuração de arquivo JSON"""
        with open(caminho_arquivo) as f:
            config = json.load(f)
        return cls(
            api_id=config['api_id'],
            api_hash=config['api_hash'],
            bot_token=config['bot_token'],
            chat_id=config['chat_id'],
            diretorio_download=Path(config['diretorio_download']),
            arquivo_m3u=Path(config['arquivo_m3u']),
            arquivo_log=Path(config['arquivo_log']),
            max_tentativas=config['max_tentativas'],
            tamanho_chunk=config['tamanho_chunk'],
            downloads_simultaneos=config['downloads_simultaneos'],
            tempo_limite_upload=config['tempo_limite_upload'],
            workers=config['workers']
        )


class GerenciadorDownload:
    """Gerencia o estado dos downloads e uploads"""
    def __init__(self):
        self.downloads_ativos: Dict[str, asyncio.Task] = {}
        self.downloads_falhos: List[EpisodeInfo] = []
        self.downloads_concluidos: List[EpisodeInfo] = []

    async def adicionar_download(self, info_episodio: EpisodeInfo, tarefa: asyncio.Task):
        self.downloads_ativos[str(info_episodio)] = tarefa
        try:
            await tarefa
            self.downloads_concluidos.append(info_episodio)
        except Exception as e:
            self.downloads_falhos.append(info_episodio)
        finally:
            del self.downloads_ativos[str(info_episodio)]

    def obter_estatisticas(self) -> Dict:
        return {
            'ativos': len(self.downloads_ativos),
            'concluidos': len(self.downloads_concluidos),
            'falhos': len(self.downloads_falhos)
        }


class MonitorVelocidade:
    """Monitora a velocidade de download com média móvel e delay inicial"""
    def __init__(self, velocidade_minima: int):
        self.velocidade_minima = velocidade_minima
        self.ultimo_tempo = time.time()
        self.ultimo_bytes = 0
        self.velocidade_atual = 0
        self.amostras_velocidade = []
        self.max_amostras = 5  # Número de amostras para média
        self.contagem_baixa_velocidade = 0
        self.max_contagem_baixa = 3  # Número de verificações consecutivas de baixa velocidade
        self.tempo_inicio = time.time()
        self.delay_inicial = 15  # Delay de 15 segundos antes de começar a verificar

    def atualizar(self, bytes_atuais: int) -> float:
        """Atualiza e retorna a velocidade atual em bytes/segundo usando média móvel"""
        tempo_atual = time.time()
        
        # Se ainda estiver dentro do período de delay inicial, apenas atualiza os bytes
        if tempo_atual - self.tempo_inicio < self.delay_inicial:
            self.ultimo_bytes = bytes_atuais
            self.ultimo_tempo = tempo_atual
            return float('inf')  # Retorna infinito para evitar detecção de velocidade baixa
        
        tempo_delta = tempo_atual - self.ultimo_tempo
        
        if tempo_delta >= 1.0:  # Atualiza a cada segundo
            bytes_delta = bytes_atuais - self.ultimo_bytes
            velocidade_instantanea = bytes_delta / tempo_delta
            
            # Adiciona a nova amostra
            self.amostras_velocidade.append(velocidade_instantanea)
            
            # Mantém apenas as últimas N amostras
            if len(self.amostras_velocidade) > self.max_amostras:
                self.amostras_velocidade.pop(0)
            
            # Calcula a média das velocidades
            self.velocidade_atual = sum(self.amostras_velocidade) / len(self.amostras_velocidade)
            
            self.ultimo_tempo = tempo_atual
            self.ultimo_bytes = bytes_atuais
            
        return self.velocidade_atual

    def velocidade_baixa(self) -> bool:
        """Verifica se a velocidade está consistentemente baixa"""
        # Se ainda estiver no período de delay inicial, retorna False
        if time.time() - self.tempo_inicio < self.delay_inicial:
            return False
            
        if self.velocidade_atual > 0 and self.velocidade_atual < self.velocidade_minima:
            self.contagem_baixa_velocidade += 1
        else:
            self.contagem_baixa_velocidade = 0
        
        return self.contagem_baixa_velocidade >= self.max_contagem_baixa


class TelegramDownloader:
    def __init__(self, config: Config):
        self.config = config
        self.config.diretorio_download.mkdir(exist_ok=True)
        self.logger = self._configurar_logging()
        self.app = Client(
            "file_uploader",
            api_id=config.api_id,
            api_hash=config.api_hash,
            bot_token=config.bot_token,
            sleep_threshold=60
        )
        self.session: Optional[aiohttp.ClientSession] = None
        self.semaforo_download = asyncio.Semaphore(config.downloads_simultaneos)
        self.gerenciador_download = GerenciadorDownload()
        self.tmdb_id_atual: Optional[str] = None

    def _configurar_logging(self) -> logging.Logger:
        """Configuração de logging com rotação de arquivos"""
        logger = logging.getLogger(__name__)
        logger.setLevel(logging.INFO)
        formatador = logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s')
        
        manipulador_arquivo = RotatingFileHandler(
            'downloader.log', 
            maxBytes=10 * 1024 * 1024, 
            backupCount=5
        )
        manipulador_arquivo.setFormatter(formatador)
        
        manipulador_console = logging.StreamHandler()
        manipulador_console.setFormatter(formatador)
        
        logger.addHandler(manipulador_arquivo)
        logger.addHandler(manipulador_console)
        return logger

    @asynccontextmanager
    async def contexto_sessao(self):
        """Gerenciador de contexto para sessão HTTP"""
        tempo_limite = aiohttp.ClientTimeout(total=self.config.tempo_limite_upload)
        async with aiohttp.ClientSession(
            timeout=tempo_limite,
            connector=aiohttp.TCPConnector(
                limit=self.config.downloads_simultaneos,
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

    async def inicializar(self):
        """Inicialização com tratamento de erros"""
        try:
            await self.app.start()
            self.logger.info("Cliente Telegram inicializado com sucesso")
        except Exception as e:
            self.logger.error(f"Erro na inicialização: {e}")
            raise

    async def limpar(self):
        """Limpeza de recursos"""
        try:
            await self.app.stop()
            self.logger.info("Recursos limpos com sucesso")
        except Exception as e:
            self.logger.error(f"Erro na limpeza: {e}")
            raise

    async def baixar_arquivo(self, url: str, caminho_arquivo: Path) -> bool:
        """Download com monitoramento de velocidade e auto-restart"""
        headers = {
            'User-Agent': 'Mozilla/5.0',
            'Connection': 'keep-alive',
            'Accept-Encoding': 'gzip, deflate'
        }
        caminho_temp = caminho_arquivo.with_suffix('.temp')
        monitor = MonitorVelocidade(self.config.velocidade_minima)
        tentativas = 0
        delay_base = 5  # Delay inicial em segundos

        async with self.semaforo_download:
            while tentativas < self.config.max_tentativas:
                try:
                    async with self.session.get(url, headers=headers) as response:
                        response.raise_for_status()
                        tamanho_total = int(response.headers.get('content-length', 0))
                        bytes_baixados = 0

                        with open(caminho_temp, 'wb') as f, tqdm(
                            desc=f"Baixando {caminho_arquivo.name}",
                            total=tamanho_total,
                            unit='iB',
                            unit_scale=True,
                            unit_divisor=1024
                        ) as barra_progresso:
                            self.logger.info("Aguardando 15 segundos para estabilização da velocidade...")
                            async for chunk in response.content.iter_chunked(self.config.tamanho_chunk):
                                if chunk:
                                    f.write(chunk)
                                    bytes_baixados += len(chunk)
                                    barra_progresso.update(len(chunk))
                                    
                                    # Monitora velocidade
                                    velocidade = monitor.atualizar(bytes_baixados)
                                    if monitor.velocidade_baixa():
                                        self.logger.warning(
                                            f"Velocidade consistentemente baixa detectada: {velocidade/1024:.2f} KB/s. "
                                            "Reiniciando download..."
                                        )
                                        raise aiohttp.ClientError("Velocidade muito baixa")

                        caminho_temp.replace(caminho_arquivo)
                        return True

                except Exception as e:
                    tentativas += 1
                    self.logger.error(f"Erro no download (tentativa {tentativas}): {e}")
                    if caminho_temp.exists():
                        caminho_temp.unlink()
                    if tentativas < self.config.max_tentativas:
                        # Aumenta o delay exponencialmente a cada tentativa
                        delay = delay_base * (2 ** (tentativas - 1))
                        self.logger.info(f"Aguardando {delay} segundos antes da próxima tentativa...")
                        await asyncio.sleep(delay)
                    continue

            return False

    async def enviar_arquivo(self, caminho_arquivo: Path, nome_arquivo: str) -> bool:
        """Upload com retry para FloodWait"""
        tamanho_arquivo = caminho_arquivo.stat().st_size
        with tqdm(
            desc=f"Enviando {nome_arquivo}",
            total=tamanho_arquivo,
            unit='iB',
            unit_scale=True,
            unit_divisor=1024
        ) as barra_progresso:
            try:
                async def progresso(atual, total):
                    barra_progresso.n = atual
                    barra_progresso.refresh()

                await self.app.send_document(
                    chat_id=self.config.chat_id,
                    document=str(caminho_arquivo),
                    file_name=nome_arquivo,
                    progress=progresso,
                    force_document=True
                )
                self.logger.info(f"Arquivo {nome_arquivo} enviado com sucesso")
                return True
            except FloodWait as e:
                self.logger.warning(f"Limite de taxa atingido. Aguardando {e.value} segundos...")
                await asyncio.sleep(e.value)
                return await self.enviar_arquivo(caminho_arquivo, nome_arquivo)
            except Exception as e:
                self.logger.error(f"Erro no upload: {e}")
                return False

    def extrair_info_episodio(self, linha_info: str, tmdb_id_atual: str):
        """Extrai número da temporada e episódio de uma linha EXTINF"""
        try:
            # Divide a linha em duas partes após a vírgula
            info_serie = linha_info.split(",", 1)[1].strip()
            
            # Tenta encontrar o padrão SxxExx no nome da série
            match = re.search(r'S(\d{1,2})E(\d{1,2})', info_serie)
            if match:
                temporada = int(match.group(1))
                episodio = int(match.group(2))
                return EpisodeInfo(
                    tmdb_id=tmdb_id_atual,
                    temporada=temporada,
                    episodio=episodio
                )
            else:
                self.logger.warning(f"Padrão de episódio não encontrado em: {info_serie}")
                return None
        except IndexError:
            self.logger.error(f"Linha mal formatada: {linha_info}")
            return None
        except Exception as e:
            self.logger.error(f"Erro inesperado ao processar linha: {linha_info}, Erro: {e}")
            return None

    def registrar_download(self, info_episodio: EpisodeInfo):
        """Log com timestamp ISO e informações do episódio"""
        with open(self.config.arquivo_log, 'a', encoding='utf-8') as log:
            timestamp = datetime.now().isoformat()
            log.write(f"{timestamp} - {info_episodio}\n")

    def ja_baixado(self, info_episodio: EpisodeInfo) -> bool:
        """Verifica se episódio já foi baixado baseado no TMDB ID, temporada e episódio"""
        if self.config.arquivo_log.exists():
            with open(self.config.arquivo_log, 'r', encoding='utf-8') as log:
                return any(str(info_episodio) in linha for linha in log)
        return False

    async def processar_url(self, url: str, info_episodio: EpisodeInfo) -> bool:
        """Processamento de URL com informações do episódio"""
        nome_arquivo = f"{info_episodio}.mp4"
        caminho_arquivo = self.config.diretorio_download / nome_arquivo
        try:
            self.logger.info(f"Iniciando download do episódio {info_episodio}")
            if not await self.baixar_arquivo(url, caminho_arquivo):
                return False
            self.logger.info(f"Iniciando upload do episódio {info_episodio}")
            sucesso = await self.enviar_arquivo(caminho_arquivo, nome_arquivo)
            if sucesso:
                self.registrar_download(info_episodio)
            return sucesso
        except Exception as e:
            self.logger.error(f"Erro processando episódio {info_episodio}: {e}")
            return False
        finally:
            if caminho_arquivo.exists():
                caminho_arquivo.unlink()
            self.logger.info(f"Arquivo {nome_arquivo} removido do disco")

    async def processar_m3u(self):
        """Processamento de M3U com verificação por TMDB ID, temporada e episódio"""
        if not self.config.arquivo_m3u.is_file():
            raise FileNotFoundError(f'Arquivo {self.config.arquivo_m3u} não encontrado')

        padrao_url = re.compile(r'^http.*\.mp4')
        padrao_tmdb = re.compile(r'#TMDB - ID \((\d+)\)')
        
        async with self.contexto_sessao():
            with open(self.config.arquivo_m3u, 'r', encoding='utf-8') as arquivo:
                linhas = arquivo.readlines()

            tmdb_id_atual = None
            info_episodio = None
            
            for linha in linhas:
                linha = linha.strip()
                
                # Procura por novo TMDB ID
                if match_tmdb := padrao_tmdb.match(linha):
                    tmdb_id_atual = match_tmdb.group(1)
                    self.logger.info(f"Encontrado novo TMDB ID: {tmdb_id_atual}")
                    continue

                # Verifica se é uma linha de informações de episódio
                if linha.startswith('#EXTINF'):
                    # Se não temos um TMDB ID, pula esta entrada
                    if not tmdb_id_atual:
                        self.logger.warning("TMDB ID não encontrado para este episódio")
                        continue
                    
                    # Extrai informações do episódio
                    info_episodio = self.extrair_info_episodio(linha, tmdb_id_atual)
                    if not info_episodio:
                        continue
                    continue

                # Verifica se a linha é um URL de vídeo
                if padrao_url.match(linha):
                    # Se não temos informações de episódio, pula
                    if not info_episodio:
                        self.logger.warning("Informações do episódio não encontradas")
                        continue

                    # Verifica se o episódio já foi baixado
                    if self.ja_baixado(info_episodio):
                        self.logger.info(f'Episódio já processado: {info_episodio}')
                        continue

                    # Adiciona o download à fila de tarefas
                    tarefa = asyncio.create_task(self.processar_url(linha, info_episodio))
                    await self.gerenciador_download.adicionar_download(info_episodio, tarefa)

            estatisticas = self.gerenciador_download.obter_estatisticas()
            self.logger.info(f"Estatísticas finais: {estatisticas}")


async def main():
    """Função principal com tratamento de erros"""
    try:
        config = Config.from_json('config.json')
        downloader = TelegramDownloader(config)
        await downloader.inicializar()
        try:
            await downloader.processar_m3u()
        finally:
            await downloader.limpar()
    except Exception as e:
        logging.error(f"Erro fatal: {e}", exc_info=True)
        raise


if __name__ == '__main__':
    # Configuração do loop de eventos para melhor performance
    loop = asyncio.new_event_loop()
    asyncio.set_event_loop(loop)
    try:
        loop.run_until_complete(main())
    finally:
        loop.close()