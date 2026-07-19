package exec

import (
	"testing"
)


func TestExtractCodeBlocksPreservesAllSupportedBlocksInSourceOrder(t *testing.T) {
	blocks := ExtractCodeBlocks("说明\n```python\nprint('first')\n```\n```javascript\nconsole.log('skip')\n```\n```shell\necho second\n```")
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v", blocks)
	}
	if blocks[0].Index != 1 || blocks[0].Language != LanguagePython || blocks[0].Code != "print('first')\n" {
		t.Fatalf("first block = %+v", blocks[0])
	}
	if blocks[1].Index != 2 || blocks[1].Language != LanguageShell || blocks[1].Code != "echo second\n" {
		t.Fatalf("second block = %+v", blocks[1])
	}
}

func TestExtractCodeBlocksIgnoresUnclosedFence(t *testing.T) {
	blocks := ExtractCodeBlocks("```python\nprint('unfinished')")
	if len(blocks) != 0 {
		t.Fatalf("blocks = %+v", blocks)
	}
}
