import re
import json
import aiohttp
import asyncio
import pandas as pd
from pathlib import Path
import os
from typing import Dict, Optional, List

class TMDBExcelConverter:
    def __init__(self):
        self.cache_file = 'tmdb_cache.json'
        self.tmdb_api_key = 'f5c93b1d4a4264da14a544c326c3e6c6'
        self.batch_size = 50
        self.load_cache()
        
    def load_cache(self):
        if os.path.exists(self.cache_file):
            with open(self.cache_file, 'r', encoding='utf-8') as f:
                self.cache = json.load(f)
        else:
            self.cache = {}

    def save_cache(self):
        with open(self.cache_file, 'w', encoding='utf-8') as f:
            json.dump(self.cache, f, indent=2)

    async def get_content_info_async(self, session, content_id: str, content_type: str) -> Optional[Dict]:
        cache_key = f"{content_type}_{content_id}"
        
        if cache_key in self.cache:
            return self.cache[cache_key]

        try:
            url = f"https://api.themoviedb.org/3/{content_type}/{content_id}"
            params = {
                'api_key': self.tmdb_api_key,
                'language': 'pt-BR'
            }
            
            async with session.get(url, params=params) as response:
                if response.status == 200:
                    content_info = {
                        'tmdb_id': int(content_id),  # Convertendo para inteiro
                        'type': content_type
                    }
                    self.cache[cache_key] = content_info
                    return content_info
        except Exception as e:
            print(f"Erro ao buscar informações para ID {content_id}: {e}")
            return None

    def parse_filename_info(self, filename: str) -> tuple:
        series_match = re.search(r'(\d+)_S(\d+)E(\d+)', filename)
        if series_match:
            content_id, season, episode = series_match.groups()
            return int(content_id), 'tv', int(season), int(episode)  # Convertendo para inteiros
        
        movie_match = re.search(r'^(\d+)', filename)
        if movie_match:
            content_id = movie_match.group(1)
            return int(content_id), 'movie', None, None  # Convertendo para inteiro
        
        return None, None, None, None

    async def process_links(self, links: List[str]):
        movies_data = []
        series_data = []
        
        async with aiohttp.ClientSession() as session:
            tasks = []
            
            for url in links:
                filename = url.split('/')[-1].split('?')[0]
                content_id, content_type, season, episode = self.parse_filename_info(filename)
                
                if content_id:
                    task = asyncio.create_task(
                        self.get_content_info_async(session, content_id, content_type)
                    )
                    tasks.append((task, content_type, season, episode, url))
            
            for i in range(0, len(tasks), self.batch_size):
                batch = tasks[i:i + self.batch_size]
                batch_results = await asyncio.gather(*(task[0] for task in batch))
                
                for result, (_, content_type, season, episode, url) in zip(batch_results, batch):
                    if result:
                        if content_type == 'movie':
                            movies_data.append({
                                'tmdb': int(result['tmdb_id']),  # Nome da coluna alterado para 'tmdb'
                                'link': url
                            })
                        else:
                            series_data.append({
                                'tmdb': int(result['tmdb_id']),  # Nome da coluna alterado para 'tmdb'
                                'temporada': int(season),
                                'episodio': int(episode),
                                'link': url
                            })
        
        return movies_data, series_data

    async def create_excel_files(self, input_file: str):
        with open(input_file, 'r', encoding='utf-8') as f:
            links = [line.strip() for line in f if line.strip()]
        
        movies_data, series_data = await self.process_links(links)
        
        # Salvando séries em um arquivo
        if series_data:
            series_df = pd.DataFrame(series_data)
            series_df.to_excel('excel/series.xlsx', index=False)
            print("Arquivo series.xlsx criado com sucesso!")
        
        # Salvando filmes em outro arquivo
        if movies_data:
            movies_df = pd.DataFrame(movies_data)
            movies_df.to_excel('excel/videos.xlsx', index=False)
            print("Arquivo videos.xlsx criado com sucesso!")
        
        self.save_cache()

async def main():
    converter = TMDBExcelConverter()
    
    input_file = 'fast_download_links.txt'
    
    print("Iniciando conversão...")
    await converter.create_excel_files(input_file)
    print("Conversão concluída!")

if __name__ == "__main__":
    asyncio.run(main())