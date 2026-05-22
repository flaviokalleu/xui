import os
import time
import random
from colorama import Fore, Back, Style, init
from pyfiglet import Figlet
import datetime

# Inicializa o colorama
init(autoreset=True)

# Configurações de estilo
TEMA = {
    "primaria": Fore.CYAN,
    "secundaria": Fore.BLUE,
    "destaque": Fore.YELLOW,
    "sucesso": Fore.GREEN,
    "erro": Fore.RED,
    "texto": Fore.WHITE,
    "subtexto": Fore.LIGHTBLACK_EX,
    "aviso": Fore.MAGENTA,
    "bordas": "═║╔╗╚╝╠╣╦╩╬"
}

# Lista de scripts organizados por categorias para melhor intuitividade
categorias = {
    "✨ CATÁLOGO": [
        {"tecla": "1", "nome": "filmes.py", "descricao": "Inserir filmes", "icone": "🎬", "atalho": "F"},
        {"tecla": "2", "nome": "series.py", "descricao": "Inserir séries", "icone": "📺", "atalho": "S"},
        {"tecla": "3", "nome": "episodios.py", "descricao": "Inserir episódios", "icone": "🎞️", "atalho": "E"},
    ],
    "📋 ORGANIZAÇÃO": [
        {"tecla": "4", "nome": "categoriafilmes.py", "descricao": "Categorias de filmes", "icone": "📂", "atalho": "C"},
        {"tecla": "5", "nome": "categoriasseries.py", "descricao": "Categorias de séries", "icone": "📁", "atalho": "G"},
        {"tecla": "6", "nome": "canais.py", "descricao": "Inserir canais", "icone": "📡", "atalho": "A"},
    ],
    "🔧 FERRAMENTAS": [
        {"tecla": "7", "nome": "gerarexcel.py", "descricao": "Exportar para Excel", "icone": "📊", "atalho": "X"},
        {"tecla": "8", "nome": "puxartelegram.py", "descricao": "Puxar do Telegram", "icone": "💬", "atalho": "T"},
    ]
}

# Mapeamento para acesso rápido (números e teclas de atalho)
mapa_scripts = {}
mapa_atalhos = {}
for categoria in categorias.values():
    for script in categoria:
        mapa_scripts[script["tecla"]] = script
        mapa_atalhos[script["atalho"].upper()] = script

# Combinando mapas para acessar por qualquer método
mapa_completo = {**mapa_scripts, **mapa_atalhos}

# Histórico de ações para sugerir opções frequentes
historico_acoes = []

def limpar_tela():
    """Limpa a tela do terminal"""
    os.system('cls' if os.name == 'nt' else 'clear')

def texto_gradiente(texto, cores):
    """Cria um texto com efeito de gradiente usando as cores fornecidas"""
    resultado = ""
    for i, char in enumerate(texto):
        cor = cores[i % len(cores)]
        resultado += cor + char
    return resultado

def desenhar_borda(largura, estilo="simples"):
    """Desenha bordas com diferentes estilos"""
    if estilo == "dupla":
        return f"{TEMA['primaria']}╔{'═' * (largura-2)}╗"
    elif estilo == "arredondada":
        return f"{TEMA['primaria']}╭{'─' * (largura-2)}╮"
    else:  # simples
        return f"{TEMA['primaria']}┌{'─' * (largura-2)}┐"

def exibir_banner():
    """Exibe um banner estilizado para o menu"""
    limpar_tela()
    fig = Figlet(font='slant')
    banner = fig.renderText('StreamMaster')
    
    # Criar efeito gradiente no banner
    cores = [Fore.CYAN, Fore.BLUE, Fore.MAGENTA, Fore.BLUE]
    banner_colorido = texto_gradiente(banner, cores)
    
    print(banner_colorido)
    
    # Barra de título com efeito de gradiente
    titulo = " SISTEMA DE GERENCIAMENTO DE STREAMING "
    padding = (60 - len(titulo)) // 2
    barra_titulo = " " * padding + titulo + " " * padding
    
    cores_titulo = [Fore.BLUE, Fore.CYAN, Fore.WHITE, Fore.CYAN, Fore.BLUE]
    print(Back.BLUE + Style.BRIGHT + texto_gradiente(barra_titulo, cores_titulo) + Style.RESET_ALL)
    
    # Data e hora atual para maior contexto
    agora = datetime.datetime.now()
    data_hora = agora.strftime("%d/%m/%Y %H:%M")
    print(f"\n{TEMA['subtexto']}Data: {data_hora}{' '*30}Versão: 3.0{Style.RESET_ALL}\n")

def exibir_menu():
    """Exibe o menu de opções com design premium e organização otimizada"""
    exibir_banner()
    
    largura_menu = 60
    
    # Sugestões com base no histórico (mostrar apenas se houver histórico)
    if historico_acoes:
        print(f"{TEMA['destaque']}⭐ AÇÕES RECENTES:{Style.RESET_ALL}")
        print(f"{TEMA['primaria']}┌{'─' * (largura_menu-2)}┐")
        
        # Pegar as 3 últimas ações distintas
        acoes_distintas = []
        for acao in reversed(historico_acoes):
            if acao not in acoes_distintas and len(acoes_distintas) < 3:
                acoes_distintas.append(acao)
        
        for acao in acoes_distintas:
            script = mapa_scripts.get(acao)
            if script:
                atalho = script["atalho"]
                print(f"{TEMA['primaria']}│ {TEMA['destaque']}[{script['tecla']}] {TEMA['sucesso']}{script['icone']} " + 
                      f"{TEMA['texto']}{script['descricao']:<32} {TEMA['subtexto']}[Alt+{atalho}]{TEMA['primaria']} │")
        
        print(f"{TEMA['primaria']}└{'─' * (largura_menu-2)}┘\n{Style.RESET_ALL}")
    
    # Exibir menu por categorias com design aprimorado
    for categoria, scripts in categorias.items():
        cor_categoria = random.choice([Fore.CYAN, Fore.YELLOW, Fore.GREEN, Fore.MAGENTA])
        print(f"{cor_categoria}{categoria}:{Style.RESET_ALL}")
        
        # Bordas superiores com estilo premium
        print(f"{TEMA['primaria']}╭{'─' * (largura_menu-2)}╮")
        
        for i, script in enumerate(scripts):
            # Aplicar destaque visual para facilitar seleção
            atalho = script["atalho"]
            estilo_linha = "│ " if i < len(scripts) - 1 else "│ "
            
            print(f"{TEMA['primaria']}{estilo_linha}{TEMA['destaque']}[{script['tecla']}] {TEMA['sucesso']}{script['icone']} " + 
                  f"{TEMA['texto']}{script['descricao']:<32} {TEMA['subtexto']}[Alt+{atalho}]{TEMA['primaria']} │")
            
            # Adicionar separador fino entre opções
            if i < len(scripts) - 1:
                print(f"{TEMA['primaria']}│ {' ' * (largura_menu-4)} │")
        
        # Bordas inferiores com estilo premium
        print(f"{TEMA['primaria']}╰{'─' * (largura_menu-2)}╯\n{Style.RESET_ALL}")
    
    # Barra de status e atalhos melhorada
    print(f"{TEMA['primaria']}┌{'─' * (largura_menu-2)}┐")
    print(f"{TEMA['primaria']}│ {TEMA['aviso']}ATALHOS RÁPIDOS:{' ' * (largura_menu-18)}{TEMA['primaria']}│")
    print(f"{TEMA['primaria']}│ {TEMA['destaque']}[0]{Style.RESET_ALL} {TEMA['erro']}🚪 Sair{' ' * 8}" + 
          f"{TEMA['destaque']}[H]{Style.RESET_ALL} {TEMA['secundaria']}❓ Ajuda{' ' * 8}" + 
          f"{TEMA['destaque']}[R]{Style.RESET_ALL} {TEMA['sucesso']}🔄 Atualizar{' ' * 5}{TEMA['primaria']}│")
    print(f"{TEMA['primaria']}└{'─' * (largura_menu-2)}┘{Style.RESET_ALL}")
    
    # Dica contextual rotativa
    dicas = [
        "💡 DICA: Use as teclas de atalho (Alt+letra) para acesso rápido",
        "💡 DICA: Categorias ajudam a encontrar as funções relacionadas",
        "💡 DICA: Exporte seus dados regularmente usando a opção Excel",
        "💡 DICA: Pressione H a qualquer momento para ver a ajuda",
    ]
    dica_atual = dicas[random.randint(0, len(dicas)-1)]
    print(f"\n{TEMA['subtexto']}{dica_atual}{Style.RESET_ALL}")

def mostrar_ajuda():
    """Exibe uma tela de ajuda premium para o usuário"""
    limpar_tela()
    print(f"{TEMA['primaria']}╔{'═' * 56}╗")
    print(f"{TEMA['primaria']}║ {TEMA['destaque']}             GUIA RÁPIDO DO SISTEMA                {TEMA['primaria']}║")
    print(f"{TEMA['primaria']}╚{'═' * 56}╝\n")
    
    # Seção principal de ajuda
    print(f"{TEMA['texto']}Como usar este sistema:\n")
    
    # Tabela de ações comuns
    print(f"{TEMA['primaria']}┌{'─' * 56}┐")
    print(f"{TEMA['primaria']}│ {TEMA['destaque']}AÇÕES BÁSICAS{' ' * 44}{TEMA['primaria']}│")
    print(f"{TEMA['primaria']}├{'─' * 56}┤")
    print(f"{TEMA['primaria']}│ {TEMA['sucesso']}• {TEMA['texto']}Digite o número ou use atalhos para acessar funções{' ' * 7}{TEMA['primaria']}│")
    print(f"{TEMA['primaria']}│ {TEMA['sucesso']}• {TEMA['texto']}Use as categorias para localizar recursos relacionados{' ' * 5}{TEMA['primaria']}│")
    print(f"{TEMA['primaria']}│ {TEMA['sucesso']}• {TEMA['texto']}Aproveite as sugestões baseadas em seu uso recente{' ' * 7}{TEMA['primaria']}│")
    print(f"{TEMA['primaria']}│ {TEMA['sucesso']}• {TEMA['texto']}Pressione 'R' a qualquer momento para atualizar a tela{' ' * 6}{TEMA['primaria']}│")
    print(f"{TEMA['primaria']}│ {TEMA['sucesso']}• {TEMA['texto']}Use 'H' para mostrar esta ajuda novamente{' ' * 18}{TEMA['primaria']}│")
    print(f"{TEMA['primaria']}│ {TEMA['sucesso']}• {TEMA['texto']}Digite '0' ou 'Q' para sair do sistema{' ' * 23}{TEMA['primaria']}│")
    print(f"{TEMA['primaria']}└{'─' * 56}┘\n")
    
    # Tabela de atalhos
    print(f"{TEMA['primaria']}┌{'─' * 56}┐")
    print(f"{TEMA['primaria']}│ {TEMA['destaque']}ATALHOS DE TECLADO{' ' * 40}{TEMA['primaria']}│")
    print(f"{TEMA['primaria']}├{'─' * 27}┬{'─' * 28}┤")
    print(f"{TEMA['primaria']}│ {TEMA['destaque']}Ação{' ' * 23}│ {TEMA['destaque']}Tecla{' ' * 23}{TEMA['primaria']}│")
    print(f"{TEMA['primaria']}├{'─' * 27}┼{'─' * 28}┤")
    
    # Listar todos os atalhos disponíveis
    for categoria, scripts in categorias.items():
        for script in scripts:
            nome_curto = script['descricao']
            if len(nome_curto) > 20:
                nome_curto = nome_curto[:18] + "..."
            print(f"{TEMA['primaria']}│ {TEMA['texto']}{nome_curto}{' ' * (24-len(nome_curto))}│ " + 
                  f"{TEMA['destaque']}[{script['tecla']}] ou Alt+{script['atalho']}{' ' * 15}{TEMA['primaria']}│")
    
    print(f"{TEMA['primaria']}└{'─' * 27}┴{'─' * 28}┘\n")
    
    # Dicas de gerenciamento
    print(f"{TEMA['destaque']}Dicas de gerenciamento:{Style.RESET_ALL}")
    print(f"{TEMA['sucesso']}1. {TEMA['texto']}Mantenha seu catálogo organizado usando as categorias")
    print(f"{TEMA['sucesso']}2. {TEMA['texto']}Faça backup regular usando a exportação para Excel")
    print(f"{TEMA['sucesso']}3. {TEMA['texto']}Use o Telegram para importar listas de conteúdo automaticamente")
    print(f"{TEMA['sucesso']}4. {TEMA['texto']}Adicione canais antes de cadastrar séries para melhor organização\n")
    
    input(f"{TEMA['aviso']}Pressione ENTER para voltar ao menu principal...{Style.RESET_ALL}")

def animacao_carregamento(texto, duracao=1.0):
    """Exibe uma animação elegante de carregamento"""
    chars = "⣾⣽⣻⢿⡿⣟⣯⣷"
    duracao_por_char = duracao / (len(chars) * 3)  # 3 ciclos completos
    
    for _ in range(3):  # 3 ciclos de animação
        for char in chars:
            print(f"\r{TEMA['secundaria']}{texto} {char}", end="", flush=True)
            time.sleep(duracao_por_char)

def executar_script(escolha):
    """Executa o script escolhido com interface premium"""
    script = mapa_completo.get(escolha)
    if script:
        # Adicionar ao histórico (apenas teclas numéricas)
        if escolha in mapa_scripts and escolha not in ["0", "H", "R"]:
            historico_acoes.append(escolha)
            # Manter apenas as últimas 10 ações no histórico
            if len(historico_acoes) > 10:
                historico_acoes.pop(0)
        
        nome_script = script["nome"]
        descricao = script["descricao"]
        icone = script["icone"]
        
        limpar_tela()
        # Cabeçalho com estilo premium
        print(f"\n{TEMA['sucesso']}╔{'═' * 55}╗")
        print(f"{TEMA['sucesso']}║ {TEMA['destaque']}{icone} Iniciando: {descricao:<38} {TEMA['sucesso']}║")
        print(f"{TEMA['sucesso']}╚{'═' * 55}╝\n")
        
        # Animação de carregamento melhorada
        print(f"{TEMA['primaria']}Preparando ambiente:", end=" ")
        for _ in range(3):
            time.sleep(0.2)
            print(f"{TEMA['destaque']}•", end=" ", flush=True)
        print(f"{TEMA['sucesso']} Concluído!\n")
        
        # Barra de progresso visual elegante
        print(f"{TEMA['primaria']}Carregando: ", end="")
        total_blocos = 20
        for i in range(total_blocos + 1):
            # Cálculo de porcentagem
            pct = i * 100 // total_blocos
            # Número de blocos preenchidos
            blocos_preenchidos = i
            # Número de espaços vazios
            espacos = total_blocos - blocos_preenchidos
            # Cor gradual baseada no progresso
            if pct < 30:
                cor = Fore.BLUE
            elif pct < 60:
                cor = Fore.CYAN
            elif pct < 90:
                cor = Fore.YELLOW
            else:
                cor = Fore.GREEN
            
            # Imprime a barra de progresso
            print(f"\r{TEMA['primaria']}Carregando: {cor}{'█' * blocos_preenchidos}{' ' * espacos} {pct}%", end="", flush=True)
            time.sleep(0.05)
        
        print(f" {TEMA['sucesso']}Completo!")
        
        try:
            print(f"\n{TEMA['aviso']}Iniciando script: {TEMA['texto']}{nome_script}{Style.RESET_ALL}")
            animacao_carregamento("Executando", 1.0)
            print()  # Linha em branco para separar
            
            # Execução com moldura visual
            print(f"{TEMA['primaria']}┌{'─' * 55}┐")
            print(f"{TEMA['primaria']}│ {TEMA['destaque']}SAÍDA DO PROGRAMA:{' ' * 38}{TEMA['primaria']}│")
            print(f"{TEMA['primaria']}└{'─' * 55}┘")
            
            os.system(f"python3 src/{nome_script}")
            
            print(f"\n{TEMA['primaria']}┌{'─' * 55}┐")
            print(f"{TEMA['primaria']}│ {TEMA['sucesso']}✓ Operação concluída com sucesso!{' ' * 26}{TEMA['primaria']}│")
            print(f"{TEMA['primaria']}└{'─' * 55}┘")
        except Exception as e:
            print(f"\n{TEMA['erro']}✗ Erro: {e}{Style.RESET_ALL}")
        
        # Mostrar ações relacionadas para maior intuitividade com design aprimorado
        print(f"\n{TEMA['destaque']}AÇÕES RELACIONADAS:{Style.RESET_ALL}")
        print(f"{TEMA['primaria']}┌{'─' * 55}┐")
        
        # Sugere outras ações da mesma categoria ou relacionadas
        contador_sugestoes = 0
        for categoria, scripts_lista in categorias.items():
            if any(s["tecla"] == escolha for s in scripts_lista) or any(s["atalho"].upper() == escolha for s in scripts_lista):
                for s in scripts_lista:
                    if s["tecla"] != escolha and contador_sugestoes < 3:  # Limita a 3 sugestões
                        print(f"{TEMA['primaria']}│ {TEMA['secundaria']}→ {TEMA['destaque']}[{s['tecla']}] {TEMA['texto']}{s['descricao']:<45}{TEMA['primaria']}│")
                        contador_sugestoes += 1
                break
        
        print(f"{TEMA['primaria']}└{'─' * 55}┘")
        
        # Botão de retorno estilizado
        print(f"\n{TEMA['primaria']}┌{'─' * 27}┐")
        print(f"{TEMA['primaria']}│ {TEMA['aviso']}[ ENTER ] Voltar ao Menu{' ' * 6}{TEMA['primaria']}│")
        print(f"{TEMA['primaria']}└{'─' * 27}┘")
        
        input("")  # Aguarda o ENTER sem texto adicional
        return True
    else:
        return False

def obter_escolha():
    """Obtém e processa a escolha do usuário com interface premium"""
    # Design da caixa de entrada
    print(f"\n{TEMA['primaria']}┌{'─' * 55}┐")
    print(f"{TEMA['primaria']}│ {TEMA['destaque']}Digite um número, atalho ou comando:{' ' * 21}{TEMA['primaria']}│")
    print(f"{TEMA['primaria']}└{'─' * 55}┘")
    
    escolha = input(f"{TEMA['sucesso']}➤ {Style.BRIGHT}").upper()
    
    # Tratamento de atalhos especiais
    if escolha.lower() in ['0', 'sair', 'exit', 'q', 'quit']:
        return "sair"
    elif escolha.upper() == 'H':
        mostrar_ajuda()
        return "continuar"
    elif escolha.upper() == 'R':
        return "continuar"
    
    # Verificar se é uma opção válida (número ou atalho)
    if escolha in mapa_completo:
        return escolha
    else:
        # Mensagem de erro com animação suave
        print(f"\r{TEMA['erro']}✗ Opção inválida.", end="")
        for _ in range(3):
            print(".", end="", flush=True)
            time.sleep(0.3)
        print(f" Digite H para ajuda.{Style.RESET_ALL}")
        time.sleep(0.7)
        return "continuar"

def encerrar_programa():
    """Encerra o programa com animação premium"""
    limpar_tela()
    
    # Animação de fechamento
    print(f"\n{TEMA['primaria']}┌{'─' * 40}┐")
    print(f"{TEMA['primaria']}│ {TEMA['destaque']}     ENCERRANDO STREAMMASTER      {TEMA['primaria']}│")
    print(f"{TEMA['primaria']}└{'─' * 40}┘\n")
    
    # Barra de progresso para encerramento
    print(f"{TEMA['primaria']}Finalizando:", end=" ")
    for i in range(10):
        print(f"{TEMA['destaque']}■", end="", flush=True)
        time.sleep(0.15)
    
    # Mensagem de despedida com estilo
    print(f"\n\n{TEMA['sucesso']}╔{'═' * 40}╗")
    print(f"{TEMA['sucesso']}║ {TEMA['texto']}Obrigado por utilizar o StreamMaster!{' ' * 8}{TEMA['sucesso']}║")
    print(f"{TEMA['sucesso']}║ {TEMA['texto']}Até a próxima! 👋{' ' * 26}{TEMA['sucesso']}║")
    print(f"{TEMA['sucesso']}╚{'═' * 40}╝")
    
    time.sleep(1.5)
    limpar_tela()

def mostrar_tela_inicial():
    """Exibe uma tela de boas-vindas ao iniciar o programa"""
    limpar_tela()
    
    # Cores para animação
    cores = [Fore.BLUE, Fore.CYAN, Fore.GREEN, Fore.YELLOW, Fore.MAGENTA]
    
    # Animação de início
    for i in range(5):
        limpar_tela()
        # Seleciona uma cor diferente a cada frame
        cor = cores[i % len(cores)]
        
        fig = Figlet(font='slant')
        banner = fig.renderText('StreamMaster')
        print(f"{cor}{banner}")
        
        # Texto de carregamento
        texto = "Iniciando sistema"
        pontos = "." * (i % 4)
        print(f"{TEMA['texto']}{texto}{pontos}{' ' * (3-len(pontos))}")
        
        time.sleep(0.3)
    
    # Tela de boas-vindas
    limpar_tela()
    fig = Figlet(font='slant')
    banner = fig.renderText('StreamMaster')
    print(texto_gradiente(banner, [Fore.CYAN, Fore.BLUE, Fore.MAGENTA, Fore.BLUE]))
    
    print(f"{TEMA['sucesso']}╔{'═' * 55}╗")
    print(f"{TEMA['sucesso']}║ {TEMA['destaque']}          BEM-VINDO AO SISTEMA STREAMMASTER          {TEMA['sucesso']}║")
    print(f"{TEMA['sucesso']}╚{'═' * 55}╝\n")
    
    print(f"{TEMA['texto']}Este sistema permite gerenciar seu catálogo de streaming com")
    print(f"{TEMA['texto']}facilidade e organização. Utilize as opções do menu para")
    print(f"{TEMA['texto']}cadastrar filmes, séries, episódios e muito mais.\n")
    
    print(f"{TEMA['primaria']}┌{'─' * 55}┐")
    print(f"{TEMA['primaria']}│ {TEMA['destaque']}[ ENTER ] Acessar o sistema{' ' * 31}{TEMA['primaria']}│")
    print(f"{TEMA['primaria']}└{'─' * 55}┘")
    
    input("")

if __name__ == "__main__":
    try:
        # Exibir tela de boas-vindas animada
        mostrar_tela_inicial()
        
        while True:
            exibir_menu()
            escolha = obter_escolha()
            
            if escolha == "sair":
                encerrar_programa()
                break
            elif escolha == "continuar":
                continue
            else:
                executar_script(escolha)
    except KeyboardInterrupt:
        encerrar_programa()