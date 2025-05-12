import os
import AshAssistant as aa
def interactive_prompt():
    print("Welcome to Ash Shell AI")
    print(os.popen("type ash.txt").read()) 
    print("\nI shall be your AI assistant. How may I assist you today?\n\n    >>> Type 'ash' for help or 'exit' to quit. <<<")
    
    ash = aa.Ash()
    #Get Required User Details like OS and local ML models available like in Ollama
    
    
    while True:
        command = input(">> ")
        AshUsed=False
        if command.lower() == 'exit':
            print("Exiting the command prompt.")
            break
        elif 'ash ' in command.lower():
            n_ashCommands=ash.process_command(command)
            for i in n_ashCommands:
                commandText, tags, tagsWithArguments = ash.extract_tags(i)
                ash.generate_shell_command(commandText, tags, tagsWithArguments, i)
            resultCommand = ash.combine_commands()
            AshUsed=True
        try:
            if AshUsed==False:
                os.system(command)
            else:
                #os.system(resultCommand)
                print(resultCommand)
                os.system("echo Got em!")
        except Exception as e:
            print(f"Error: {e}")

if __name__ == "__main__":
    interactive_prompt()