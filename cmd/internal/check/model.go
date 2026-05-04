package check

import (
	"fmt"
	"os"
)

func ModelExists(path string) {
	if _, err := os.Stat(path); err == nil {
		return
	}

	fmt.Fprintf(os.Stderr, `Error: model file not found: %s

The Silero VAD model (silero_vad.onnx) is required.

How to resolve:

  1. Run the setup script (recommended):
     ./scripts/setup.sh

  2. Or install manually:
     Download the model from the official Silero VAD repo:
       curl -LO https://github.com/snakers4/silero-vad/raw/master/files/silero_vad.onnx

     Or via Python:
       pip install silero-vad
       cp $(python3 -c "import silero_vad, os; print(os.path.join(os.path.dirname(silero_vad.__file__),'data','silero_vad.onnx'))") .

  3. Use -model flag to point to an existing model file:
     %s -model /path/to/silero_vad.onnx

`, path, os.Args[0])
	os.Exit(1)
}
