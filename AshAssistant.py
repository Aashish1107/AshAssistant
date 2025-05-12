import re

class Ash:
    def __init__(self):
        self.command_prefix = "ash "
        self.commands=[]
        self.input_text_pos=[]
        self.command_is_pipe=False

    def process_command(self, command):
        """
        Processes the input text to extract the Ash command and tags.
        """
    
        self.command_is_pipe=False
        len_input_text=0
        self.input_text_pos=[]
        # If the command contains a pipe, extract the part before the pipe
        if "|" in command:
            self.command_is_pipe=True
            self.commands = command.split(" | ")
            for i in range(len(self.commands)):
                len_input_text+=1
                if self.commands[i].startswith(self.command_prefix):
                    self.input_text_pos.append(i)
                             
        if self.command_is_pipe==False:
            self.commands = [command]
            self.input_text_pos.append(0)
            len_input_text=1
            
        return self.input_text_pos
        
    def extract_tags(self, index):     
        # Extract the command text from the input text at the specified index
        command_text = self.commands[index][len(self.command_prefix):].strip()
        self.commands[index]=command_text

        # Extract tags and their arguments from the command (e.g., '-h', '--help', '--file example.txt')
        tagsWithArguments = re.findall(r"--?\w+\s+[^-]+", command_text)  # Matches tags with arguments
        for tagsWithArgument in tagsWithArguments:
            command_text = command_text.replace(tagsWithArgument, "")
            
        tags = re.findall(r"--?\w+", command_text)  # Matches tags like -h or --help
        
        #Arrange tags in order, tags with "--" come first, then tags with "-"
        tags = sorted(tags, key=len, reverse=True)  # Sort by length
        
        for tag in tags:
            command_text = command_text.replace(tag, "")
        # Clean up the command_text by stripping extra spaces
        command_text = command_text.strip()
        
        return (command_text, tags, tagsWithArguments)
        
    def combine_commands(self):
        command = self.commands[0]
        if self.command_is_pipe==True:
            for i in range(1,len(self.commands)):
                command = command + " | " + self.commands[i]
        return command

    def generate_shell_command(self, text, tags, tagsWithArguments, index=0):
        """
        Generates a shell command based on the input text.
        """
        # Example: You can customize this logic to map commands to shell commands
        if text.startswith("'list files"):
            self.commands[index]="dir"
        else:
            return f"echo 'Couldnt Generate command for {command}'"

# Example usage
if __name__ == "__main__":
    ash = Ash()
    input_text = "ash list files | grep .py"
    command, tags = ash.process_command(input_text)
    if command:
        shell_command = ash.generate_shell_command(command)
        print(f"Shell Command: {shell_command}")
        print(f"Tags: {tags}")
    else:
        print("No valid Ash command detected.")