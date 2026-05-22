from telethon.sync import TelegramClient
from telethon.tl.types import KeyboardButtonUrl
import asyncio
from tqdm import tqdm
import time
import re

# Credenciais da API
api_id = '22594796'
api_hash = 'ef1f82927273518ebb576665d7c63a55'
session_name = 'session'

async def get_total_messages(client, group):
    """Função para contar o total de mensagens no grupo"""
    return sum(1 for _ in await client.get_messages(group, limit=None))

async def fetch_download_links(group_username, limit=None):
    async with TelegramClient(session_name, api_id, api_hash) as client:
        try:
            # Buscar o grupo pelo username
            print("Conectando ao grupo...")
            group = await client.get_entity(group_username)
            
            # Contar total de mensagens
            print("Contando mensagens totais...")
            total_messages = await get_total_messages(client, group)
            print(f"Total de mensagens encontradas: {total_messages}")
            
            # Lista para armazenar os links de download
            download_links = []
            
            # Configurar barra de progresso
            pbar = tqdm(total=total_messages, desc="Processando mensagens")
            
            # Contador para mostrar status periodicamente
            last_update = time.time()
            messages_processed = 0

            # Iterar pelas mensagens do grupo
            async for message in client.iter_messages(group, limit=limit):
                messages_processed += 1
                pbar.update(1)
                
                # Atualizar status a cada 5 segundos
                current_time = time.time()
                if current_time - last_update >= 5:
                    print(f"\nStatus: {messages_processed}/{total_messages} mensagens processadas")
                    print(f"Links encontrados até agora: {len(download_links)}")
                    last_update = current_time

                if message.reply_markup:  # Verificar se a mensagem tem botões
                    for row in message.reply_markup.rows:
                        for button in row.buttons:
                            try:
                                # Imprimir o texto do botão para debug
                                print(f"\nTexto do botão encontrado: {button.text}")
                                
                                # Verificar se é um botão de URL e contém 'Fast Download' em qualquer parte do texto
                                if (isinstance(button, KeyboardButtonUrl) and 
                                    'Fast Download' in button.text):
                                    download_links.append(button.url)
                                    print(f"Link encontrado: {button.url}")
                            except Exception as e:
                                print(f"Erro ao processar botão: {str(e)}")
                                continue
                                
            pbar.close()

            # Salvar os links em um arquivo txt
            print("\nSalvando links no arquivo...")
            with open('fast_download_links.txt', 'w', encoding='utf-8') as f:
                for link in download_links:
                    f.write(f"{link}\n")

            print(f"\nProcessamento finalizado!")
            print(f"Total de {len(download_links)} links encontrados e salvos em 'fast_download_links.txt'")
            
            # Mostrar os primeiros e últimos 5 links como amostra
            if download_links:
                print("\nPrimeiros 5 links encontrados:")
                for i, link in enumerate(download_links[:5], 1):
                    print(f"{i}. {link}")
                    
                if len(download_links) > 5:
                    print("\nÚltimos 5 links encontrados:")
                    for i, link in enumerate(download_links[-5:], len(download_links)-4):
                        print(f"{i}. {link}")
            else:
                print("\nNenhum link de download encontrado.")

            # Adicionar verificação de duplicatas
            unique_links = set(download_links)
            if len(unique_links) != len(download_links):
                print(f"\nAtenção: Foram encontrados {len(download_links) - len(unique_links)} links duplicados")
                
                # Salvar links únicos em um arquivo separado
                with open('fast_download_links_unique.txt', 'w', encoding='utf-8') as f:
                    for link in unique_links:
                        f.write(f"{link}\n")
                print(f"Links únicos foram salvos em 'fast_download_links_unique.txt'")

        except Exception as e:
            print(f"Erro ao processar o grupo: {str(e)}")
        finally:
            if 'pbar' in locals():
                pbar.close()

def main():
    # Username do grupo (sem o @)
    group_username = "asuasdudhashuasd"  # Substitua pelo username correto
    
    # Número máximo de mensagens para processar (None para todas as mensagens)
    message_limit = None  # Altere para um número se quiser limitar
    
    print("Iniciando extração de links...")
    # Executar o script
    asyncio.run(fetch_download_links(group_username, message_limit))

if __name__ == "__main__":
    main()