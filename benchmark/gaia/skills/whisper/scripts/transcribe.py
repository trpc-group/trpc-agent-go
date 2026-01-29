#!/usr/bin/env python3
"""
Whisper Audio Transcription Script
Transcribe audio files to text using OpenAI Whisper
"""

import argparse
import json
import sys
import os

try:
    import whisper
except ImportError:
    print("Error: openai-whisper not installed. Run: pip install openai-whisper", file=sys.stderr)
    sys.exit(1)


def transcribe_audio(audio_path, output_path, model_size="base", language=None, 
                     timestamps=False, output_format="text"):
    """Transcribe audio file using Whisper"""
    
    # Validate input file
    if not os.path.exists(audio_path):
        print(f"Error: Audio file not found: {audio_path}", file=sys.stderr)
        sys.exit(1)
    
    # Load Whisper model
    print(f"Loading Whisper model: {model_size}...", file=sys.stderr)
    model = whisper.load_model(model_size)
    
    # Transcribe
    print(f"Transcribing: {audio_path}...", file=sys.stderr)
    
    transcribe_options = {}
    if language and language != "auto":
        transcribe_options["language"] = language
    
    result = model.transcribe(audio_path, **transcribe_options)
    
    # Write output
    if output_format == "json":
        # JSON format with full metadata
        output_data = {
            "text": result["text"].strip(),
            "language": result.get("language", "unknown"),
            "segments": []
        }
        
        if timestamps and "segments" in result:
            for seg in result["segments"]:
                output_data["segments"].append({
                    "start": seg["start"],
                    "end": seg["end"],
                    "text": seg["text"].strip()
                })
        
        with open(output_path, "w", encoding="utf-8") as f:
            json.dump(output_data, f, ensure_ascii=False, indent=2)
    else:
        # Plain text format
        text = result["text"].strip()
        
        if timestamps and "segments" in result:
            lines = []
            for seg in result["segments"]:
                lines.append(f"[{seg['start']:.2f}s - {seg['end']:.2f}s] {seg['text'].strip()}")
            text = "\n".join(lines)
        
        with open(output_path, "w", encoding="utf-8") as f:
            f.write(text)
    
    print(f"✓ Transcription saved to: {output_path}", file=sys.stderr)
    print(f"  Language: {result.get('language', 'unknown')}", file=sys.stderr)
    print(f"  Text length: {len(result['text'])} characters", file=sys.stderr)


def main():
    parser = argparse.ArgumentParser(description="Transcribe audio using Whisper")
    parser.add_argument("audio_file", help="Input audio file path")
    parser.add_argument("output_file", help="Output text/JSON file path")
    parser.add_argument("--model", default="base", 
                       choices=["tiny", "base", "small", "medium", "large"],
                       help="Whisper model size (default: base)")
    parser.add_argument("--language", default="auto",
                       help="Language code (e.g., en, zh, es) or 'auto' (default: auto)")
    parser.add_argument("--timestamps", action="store_true",
                       help="Include timestamps in output")
    parser.add_argument("--format", default="text", choices=["text", "json"],
                       help="Output format (default: text)")
    
    args = parser.parse_args()
    
    transcribe_audio(
        args.audio_file,
        args.output_file,
        model_size=args.model,
        language=args.language if args.language != "auto" else None,
        timestamps=args.timestamps,
        output_format=args.format
    )


if __name__ == "__main__":
    main()
