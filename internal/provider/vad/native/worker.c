/* Fixed-frame PCM16 adapter to the installed Sherpa Silero VAD API. */
#include <windows.h>
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <fcntl.h>
#include <io.h>
#include "sherpa-onnx/c-api/c-api.h"
static void *symbol(HMODULE dll,const char *name){void *p=(void *)GetProcAddress(dll,name);if(!p){fprintf(stderr,"Missing %s\n",name);exit(2);}return p;}
int main(int argc,char **argv){
 if(argc!=3)return 2;
 _setmode(_fileno(stdin),_O_BINARY);_setmode(_fileno(stdout),_O_BINARY);
 SetDllDirectoryA(argv[1]);char dllpath[4096];snprintf(dllpath,sizeof(dllpath),"%s\\sherpa-onnx-c-api.dll",argv[1]);
 HMODULE dll=LoadLibraryA(dllpath);if(!dll){fprintf(stderr,"Load VAD API: %lu\n",GetLastError());return 2;}
 const SherpaOnnxVoiceActivityDetector *(*create)(const SherpaOnnxVadModelConfig *,float)=symbol(dll,"SherpaOnnxCreateVoiceActivityDetector");
 void (*accept)(const SherpaOnnxVoiceActivityDetector *,const float *,int32_t)=symbol(dll,"SherpaOnnxVoiceActivityDetectorAcceptWaveform");
 int32_t (*detected)(const SherpaOnnxVoiceActivityDetector *)=symbol(dll,"SherpaOnnxVoiceActivityDetectorDetected");
 void (*clear)(const SherpaOnnxVoiceActivityDetector *)=symbol(dll,"SherpaOnnxVoiceActivityDetectorClear");
 void (*destroy)(const SherpaOnnxVoiceActivityDetector *)=symbol(dll,"SherpaOnnxDestroyVoiceActivityDetector");
 SherpaOnnxVadModelConfig config={0};config.silero_vad.model=argv[2];config.silero_vad.threshold=0.5f;config.silero_vad.min_silence_duration=0.05f;config.silero_vad.min_speech_duration=0.1f;config.silero_vad.max_speech_duration=29;config.silero_vad.window_size=512;config.sample_rate=16000;config.num_threads=1;config.provider="cpu";
 const SherpaOnnxVoiceActivityDetector *vad=create(&config,30);if(!vad)return 3;
 uint32_t magic=0x56414431;if(fwrite(&magic,4,1,stdout)!=1)return 4;fflush(stdout);
 int16_t pcm[320];float samples[320];
 size_t n;
 while((n=fread(pcm,2,320,stdin))==320){
  for(int i=0;i<320;i++)samples[i]=(float)pcm[i]/32768;
  accept(vad,samples,320);uint32_t speech=detected(vad)?1:0;
  clear(vad);if(fwrite(&speech,4,1,stdout)!=1)return 4;fflush(stdout);
 }
 destroy(vad);FreeLibrary(dll);return n==0?0:4;
}
