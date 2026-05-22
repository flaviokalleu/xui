import re
from collections import defaultdict
import json
import os
import mysql.connector
from datetime import datetime, timedelta

class M3UProcessor:
    def __init__(self):
        # Inicializa todas as propriedades da classe
        self.db_config = self.load_db_config()
        
        # Mapeamento de categorias
        self.categories_map = {
            "CANAIS GLOBO": ["Rede Globo", "GLOBO PLAY", "globoplay", "globo play", "GlOBO Play", "GlOBO PLAY", "Globoplay"],
            "CANAIS SBT": ["SBT", "Sistema Brasileiro de Televisão"],
            "CANAIS RECORD": ["Record"],
            "CANAIS TELECINE": ["Telecine"],
            "CANAIS BAND": ["Band", "BandNews FM", "BAND"],
            "CANAIS Cinemax": ["Cinemax"],
            "Canais de TV Aberta": ["RedeTV", "Record", "SBT", "TV Cultura", "REDE TV", "Rede Gazeta", "Futura", "TV Brasil"],
            "Canais de Esportes": ["Sport TV", "ESPN", "FOX Sports", "SPORT TV", "Copa Libertadores", "UEFA Europa League", "Band Sports"],
            "Canais de Filmes e Séries": ["HBO", "Warner TV", "Telecine", "Universal TV", "Starz", "Studio Universal", "Rede VIVA", "Paramount Pictures"],
            "Canais Infantis": ["Cartoon Network", "Nick at Nite", "Nick Jr", "Tooncast", "BabyTV"],
            "Canais de Entretenimento": ["Comedy Central", "MTV", "E!", "AMC Networks", "TNT", "Film&Arts"],
            "Canais de Documentários e Ciência": ["GNT", "Discovery Channel", "Discovery", "National Geographic", "Discovery Science", "Investigation Discovery", "TLC", "Science"],
            "Canais de Lifestyle e Culinária": ["Food Network", "HGTV", "A&E Network", "Space", "Canal OFF"],
            "Canais Religiosos": ["Canção Nova", "Legião da Boa Vontade", "Rede Vida", "Rede Aparecida", "Viva"],
            "Canais de Notícias": ["MSNBC", "Globo Play", "Fox News", "GAZETA TV", "CNN"],
            "Canais de Música": ["MTV", "Nick at Nite"]
        }

        # Valores padrão para novas categorias
        self.default_category_values = {
            "category_type": "live",
            "visible": 1,
            "bouquet": 0
        }
        
        # Inicializa outras propriedades
        self.streams = defaultdict(list)
        self.conn = None
        self.cursor = None
        self.next_stream_id = None
        # Armazenar mapeamento de categorias encontradas no banco
        self.db_categories = {}
        # Armazenar streams existentes no banco
        self.existing_streams = {}
        # Nova lista para armazenar todos os IDs de canais processados
        self.processed_stream_ids = []

    def load_db_config(self):
        """Carrega a configuração do banco de dados"""
        try:
            with open("db/db_config.json", "r") as file:
                return json.load(file)
        except Exception as e:
            print(f"Erro ao carregar configuração do banco de dados: {e}")
            return None  # ou um dicionário padrão

    def connect_db(self):
        """Estabelece conexão com o banco de dados"""
        try:
            self.conn = mysql.connector.connect(**self.db_config)
            self.cursor = self.conn.cursor(dictionary=True)
            print("Conexão com o banco de dados estabelecida com sucesso")
        except Exception as e:
            print(f"Erro ao conectar ao banco de dados: {e}")
            raise

    def close_db(self):
        """Fecha a conexão com o banco de dados"""
        if self.cursor:
            self.cursor.close()
        if self.conn:
            self.conn.close()
            print("Conexão com o banco de dados fechada")

    def get_next_stream_id(self):
        """Obtém o próximo ID disponível para streams"""
        try:
            self.cursor.execute("SELECT MAX(id) as max_id FROM streams")
            result = self.cursor.fetchone()
            next_id = (result['max_id'] or 0) + 1
            print(f"Próximo ID de stream: {next_id}")
            return next_id
        except Exception as e:
            print(f"Erro ao obter próximo ID: {e}")
            return 4291

    def get_next_category_id(self):
        """Obtém o próximo ID disponível para categorias"""
        try:
            self.cursor.execute("SELECT MAX(id) as max_id FROM streams_categories")
            result = self.cursor.fetchone()
            next_id = (result['max_id'] or 0) + 1
            print(f"Próximo ID de categoria: {next_id}")
            return next_id
        except Exception as e:
            print(f"Erro ao obter próximo ID de categoria: {e}")
            return 50  # ID base para novas categorias

    def read_playlist_file(self, file_path):
        """Lê o arquivo M3U ou M3U8"""
        try:
            with open(file_path, 'r', encoding='utf-8') as file:
                content = file.read()
                print(f"Arquivo playlist lido com sucesso: {file_path}")
                return content
        except Exception as e:
            print(f"Erro ao ler arquivo playlist: {e}")
            return ""

    def get_base_channel_name(self, channel_name):
        """Extrai o nome base do canal e mantém apenas palavras-chave relevantes."""
        base_name = channel_name.lower()

        # Se for um canal Globo, manter apenas "Rede Globo" ou "Globo Play"
        if "rede globo" in base_name:
            return "Rede Globo"
        if "lobo" in base_name:
            return "Rede Globo"
        if "globo play" in base_name:
            return "Globo Play"

        # Caso contrário, remover números e palavras "CANAL"
        base_name = re.sub(r'\s*CANAL\s*\d+', '', channel_name, flags=re.IGNORECASE)
        base_name = re.sub(r'\s*\d+', '', base_name)  # Remove números soltos
        base_name = base_name.strip()
        
        
        return base_name

    def load_existing_categories(self):
        """Carrega as categorias existentes do banco de dados"""
        try:
            self.cursor.execute("SELECT id, category_name FROM streams_categories")
            categories = self.cursor.fetchall()
            self.db_categories = {category['category_name']: category['id'] for category in categories}
            print(f"Categorias carregadas do banco: {len(self.db_categories)}")
            return self.db_categories
        except Exception as e:
            print(f"Erro ao carregar categorias existentes: {e}")
            return {}
    
    def load_existing_streams(self):
        """Carrega os streams existentes do banco de dados"""
        try:
            self.cursor.execute("SELECT id, stream_display_name FROM streams")
            streams = self.cursor.fetchall()
            self.existing_streams = {stream['stream_display_name']: stream['id'] for stream in streams}
            print(f"Streams carregados do banco: {len(self.existing_streams)}")
            return self.existing_streams
        except Exception as e:
            print(f"Erro ao carregar streams existentes: {e}")
            return {}

    def get_or_create_category(self, category_name):
        """Verifica se a categoria existe e cria se não existir"""
        # Se ainda não carregamos as categorias, carregar agora
        if not self.db_categories:
            self.load_existing_categories()
        
        # Verificar se a categoria já existe no banco
        if category_name in self.db_categories:
            print(f"Categoria '{category_name}' já existe (ID: {self.db_categories[category_name]})")
            return self.db_categories[category_name]
        
        # Se não existe, criar nova categoria
        try:
            next_id = self.get_next_category_id()
            sql = """
            INSERT INTO streams_categories 
            (id, category_type, category_name, parent_id, cat_order, is_adult) 
            VALUES (%s, %s, %s, %s, %s, %s)
            """
            params = (
                next_id, 
                self.default_category_values['category_type'],
                category_name,
                0,  # parent_id
                next_id,  # cat_order
                0   # is_adult
            )
            self.execute_sql(sql, params)
            
            # Adicionar ao dicionário de categorias carregadas
            self.db_categories[category_name] = next_id
            print(f"Nova categoria '{category_name}' criada com ID {next_id}")
            return next_id
        except Exception as e:
            print(f"Erro ao criar categoria '{category_name}': {e}")
            return 33  # ID padrão em caso de erro

    def identify_category_name(self, channel_name):
        """Identifica a qual categoria o canal pertence baseado no nome"""
        for category, patterns in self.categories_map.items():
            for pattern in patterns:
                if pattern.lower() in channel_name.lower():
                    print(f"Canal '{channel_name}' corresponde à categoria '{category}'")
                    return category
        
        print(f"Canal '{channel_name}' sem correspondência exata, atribuindo categoria 'Outros Canais'")
        return "Outros Canais"

    def get_category_id(self, channel_name, explicit_category=None):
        """Determina a categoria do canal baseado no nome e garante que ela existe no banco
        
        Args:
            channel_name: Nome do canal
            explicit_category: Categoria explícita (tvg-id), se disponível
        """
        # Se houver uma categoria explícita, usar ela
        if explicit_category:
            category_id = self.get_or_create_category(explicit_category)
            print(f"Canal '{channel_name}' categorizado explicitamente como '{explicit_category}' (ID {category_id})")
            return category_id
        
        # Caso contrário, usar o método de identificação por nome
        category_name = self.identify_category_name(channel_name)
        category_id = self.get_or_create_category(category_name)
        print(f"Canal '{channel_name}' categorizado como '{category_name}' (ID {category_id})")
        return category_id

    def parse_playlist(self, playlist_content):
        """Processa o conteúdo do arquivo M3U ou M3U8"""
        lines = playlist_content.strip().split('\n')
        
        current_name = None
        current_logo = None
        current_category = None
        
        for i in range(len(lines)):
            line = lines[i].strip()
            
            if line.startswith('#EXTINF'):
                # Processar informações do canal
                name_match = re.search('tvg-name="([^"]+)"', line)
                logo_match = re.search('tvg-logo="([^"]+)"', line)
                
                # Verificar se existe tvg-id para usar como categoria
                category_match = re.search('tvg-id="([^"]+)"', line)
                
                if name_match:
                    current_name = name_match.group(1)
                else:
                    # Tentar extrair o nome após a vírgula (formato alternativo)
                    comma_match = re.search(',(.+)$', line)
                    if comma_match:
                        current_name = comma_match.group(1).strip()
                
                if logo_match:
                    current_logo = logo_match.group(1)
                
                if category_match:
                    current_category = category_match.group(1)
                    print(f"Categoria encontrada: {current_category} para canal: {current_name}")
            
            # Se a linha começa com http, é a URL do stream
            elif line.startswith('http'):
                if current_name:
                    base_name = self.get_base_channel_name(current_name)
                    # Agrupar múltiplas URLs para o mesmo canal
                    self.streams[base_name].append({
                        'name': current_name,
                        'logo': current_logo,
                        'url': line,
                        'category': current_category
                    })
                    
                    # Limpar variáveis para o próximo canal
                    current_name = None
                    current_logo = None
                    current_category = None

    def execute_sql(self, sql, params=None):
        """Executa uma query SQL com tratamento de erro"""
        try:
            if params:
                self.cursor.execute(sql, params)
            else:
                self.cursor.execute(sql)
            self.conn.commit()
        except Exception as e:
            print(f"Erro ao executar SQL: {e}")
            self.conn.rollback()
            raise

    def insert_or_update_stream(self, base_name, category_id, urls, logo, expiry_date):
        """Insere um novo stream ou atualiza um existente no banco de dados"""
        try:
            # Verificar se o stream já existe
            if base_name in self.existing_streams:
                stream_id = self.existing_streams[base_name]
                print(f"Atualizando stream existente '{base_name}' (ID: {stream_id})")
                
                formatted_urls = "[" + ",".join(['"{}"'.format(url.replace("/", "\\/")) for url in urls]) + "]"
                escaped_name = base_name.replace("'", "''")
                escaped_logo = logo.replace("'", "''") if logo else ""
                
                # Atualizar stream existente
                sql_update = f"""
                UPDATE streams 
                SET 
                    stream_source = '{formatted_urls}',
                    category_id = '[{category_id}]',
                    stream_icon = '{escaped_logo}'                    
                WHERE id = {stream_id}
                """
                self.execute_sql(sql_update)
                
                # Verificar se é necessário atualizar na tabela streams_servers
                self.cursor.execute(f"SELECT server_stream_id FROM streams_servers WHERE stream_id = {stream_id}")
                server_exists = self.cursor.fetchone()
                
                if not server_exists:
                    # Inserir na tabela streams_servers se não existir
                    sql_servers = f"""
                    INSERT INTO streams_servers (
                        server_stream_id, stream_id, server_id, parent_id, pid, to_analyze, stream_status,
                        stream_started, stream_info, monitor_pid, aes_pid, current_source, bitrate, 
                        progress_info, cc_info, on_demand, delay_pid, delay_available_at, pids_create_channel, 
                        cchannel_rsources, updated, compatible, audio_codec, video_codec, resolution, ondemand_check
                    ) VALUES (
                        NULL, {stream_id}, '1', NULL, NULL, '0', '0', NULL, NULL, NULL, NULL, NULL, NULL, 
                        NULL, NULL, '0', NULL, NULL, NULL, NULL, NOW(), '0', NULL, NULL, NULL, NULL
                    )
                    """
                    self.execute_sql(sql_servers)
                
                # Adicionar o ID à lista de IDs processados
                self.processed_stream_ids.append(stream_id)
                
                print(f"Stream '{base_name}' atualizado com sucesso!")
                return stream_id, False  # False indica que não é um novo stream
            
            # Se o stream não existe, inserir novo
            stream_id = self.next_stream_id
            formatted_urls = "[" + ",".join(['"{}"'.format(url.replace("/", "\\/")) for url in urls]) + "]"
            escaped_name = base_name.replace("'", "''")
            escaped_logo = logo.replace("'", "''") if logo else ""

            sql_stream = f"""INSERT INTO `streams` VALUES (
                {stream_id},
                1,
                '[{category_id}]',
                '{escaped_name}',
                '{formatted_urls}',
                '{escaped_logo}',
                NULL,0,NULL,NULL,NULL,NULL,0,NULL,0,0,NULL,1,0,'',NULL,
                {stream_id + 168},
                '',0,0,{expiry_date},0,1,0,0,0,0,0,0,0,0,128000,NULL,'{{}}',0,
                NULL,0,NULL,0,'\'\'',
                NULL,0,'0000-00-00 00:00:00',NULL,NULL,NULL,NULL,0,90,1
            );"""

            self.execute_sql(sql_stream)

            # Inserir na tabela streams_servers
            sql_servers = f"""INSERT INTO `streams_servers` (
                `server_stream_id`, `stream_id`, `server_id`, `parent_id`, `pid`, `to_analyze`, `stream_status`,
                `stream_started`, `stream_info`, `monitor_pid`, `aes_pid`, `current_source`, `bitrate`, 
                `progress_info`, `cc_info`, `on_demand`, `delay_pid`, `delay_available_at`, `pids_create_channel`, 
                `cchannel_rsources`, `updated`, `compatible`, `audio_codec`, `video_codec`, `resolution`, `ondemand_check`
            ) VALUES (
                NULL, {stream_id}, '1', NULL, NULL, '0', '0', NULL, NULL, NULL, NULL, NULL, NULL, 
                NULL, NULL, '0', NULL, NULL, NULL, NULL, NOW(), '0', NULL, NULL, NULL, NULL
            );"""

            self.execute_sql(sql_servers)

            # Adicionar ao dicionário de streams existentes
            self.existing_streams[base_name] = stream_id
            
            # Adicionar o ID à lista de IDs processados
            self.processed_stream_ids.append(stream_id)
            
            self.next_stream_id += 1
            
            print(f"Novo stream '{base_name}' inserido com sucesso! ID: {stream_id}")
            return stream_id, True  # True indica que é um novo stream
        except Exception as e:
            print(f"Erro ao inserir/atualizar stream {base_name}: {e}")
            return None, False

    def update_canais_bouquet(self):
        """Atualiza ou cria o bouquet 'CANAIS' com todos os IDs de canais processados"""
        try:
            # Verificar se temos IDs processados
            if not self.processed_stream_ids:
                print("Nenhum canal foi processado para atualizar o bouquet")
                return False
            
            # Verificar se o bouquet 'CANAIS' já existe
            self.cursor.execute("SELECT id, bouquet_channels FROM bouquets WHERE bouquet_name = 'CANAIS'")
            canais_bouquet = self.cursor.fetchone()
            
            # Converter a lista de IDs para formato JSON
            channels_json = json.dumps(self.processed_stream_ids)
            
            if canais_bouquet:
                # Bouquet existe, atualizar
                bouquet_id = canais_bouquet['id']
                
                # Obter os canais existentes
                existing_channels = json.loads(canais_bouquet['bouquet_channels'])
                
                # Adicionar apenas os novos IDs que não existem no bouquet
                for stream_id in self.processed_stream_ids:
                    if stream_id not in existing_channels:
                        existing_channels.append(stream_id)
                
                # Atualizar bouquet com a lista combinada
                updated_channels_json = json.dumps(existing_channels)
                
                self.cursor.execute(
                    "UPDATE bouquets SET bouquet_channels = %s WHERE id = %s",
                    (updated_channels_json, bouquet_id)
                )
                print(f"Bouquet 'CANAIS' atualizado com {len(existing_channels)} canais (adicionados {len(self.processed_stream_ids)} novos canais)")
            else:
                # Bouquet não existe, criar novo
                self.cursor.execute(
                    """INSERT INTO bouquets 
                    (bouquet_name, bouquet_channels, bouquet_movies, bouquet_radios, bouquet_series, bouquet_order) 
                    VALUES (%s, %s, '[]', '[]', '[]', %s)""",
                    ('CANAIS', channels_json, 0)
                )
                print(f"Bouquet 'CANAIS' criado com {len(self.processed_stream_ids)} canais")
            
            self.conn.commit()
            return True
        except Exception as e:
            print(f"Erro ao atualizar bouquet 'CANAIS': {e}")
            self.conn.rollback()
            return False

    def process(self):
        """Processo principal de execução"""
        print("Iniciando processamento...")

        # Define o caminho do diretório de playlists
        playlist_dir = os.path.join(os.getcwd(), "m3u")

        # Verifica se o diretório existe
        if not os.path.exists(playlist_dir):
            print(f"Diretório {playlist_dir} não encontrado.")
            return

        # Procura por arquivos .m3u e .m3u8 dentro da pasta m3u
        playlist_files = [f for f in os.listdir(playlist_dir) if f.endswith(('.m3u', '.m3u8'))]

        print(f"Arquivos encontrados: {playlist_files}")

        if not playlist_files:
            print("Nenhum arquivo .m3u ou .m3u8 encontrado no diretório ./m3u")
            return

        try:
            # Conecta ao banco de dados
            self.connect_db()

            # Carrega categorias e streams existentes
            self.load_existing_categories()
            self.load_existing_streams()

            # Obtém o próximo ID disponível
            self.next_stream_id = self.get_next_stream_id()

            # Inicializa a lista de IDs processados
            self.processed_stream_ids = []

            expiry_date = int((datetime.now() + timedelta(days=365)).timestamp())

            # Processar cada arquivo encontrado
            for input_file in playlist_files:
                input_file_path = os.path.join(playlist_dir, input_file)
                print(f"\nProcessando arquivo: {input_file}")

                # Lê e processa o arquivo de playlist
                playlist_content = self.read_playlist_file(input_file_path)
                if not playlist_content:
                    print(f"Conteúdo da playlist {input_file} vazio, pulando...")
                    continue

                print(f"Processando conteúdo da playlist {input_file}...")

                # Limpar streams entre arquivos para evitar duplicações durante o processamento
                self.streams = defaultdict(list)

                self.parse_playlist(playlist_content)

                # Insere ou atualiza streams
                print(f"Inserindo/atualizando streams de {input_file}...")

                for base_name, streams in self.streams.items():
                    urls = [stream['url'] for stream in streams]
                    logo = streams[0]['logo'] if streams[0]['logo'] else None

                    # Verificar se existe uma categoria explícita para este canal
                    explicit_category = streams[0].get('category')
                    category_id = self.get_category_id(base_name, explicit_category)

                    stream_id, is_new = self.insert_or_update_stream(base_name, category_id, urls, logo, expiry_date)

            print(f"\nAtualizando bouquet 'CANAIS' com {len(self.processed_stream_ids)} IDs de canais processados...")
            self.update_canais_bouquet()

            print(f"\nProcessamento concluído! {len(self.processed_stream_ids)} streams processados.")
            print(f"Total de categorias utilizadas: {len(self.db_categories)}")

        except Exception as e:
            print(f"Erro durante o processamento: {e}")
        finally:
            self.close_db()

if __name__ == "__main__":
    processor = M3UProcessor()
    processor.process()