/* Small binary-protocol adapter to the existing sherpa-onnx C API. */
#include <windows.h>
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <fcntl.h>
#include <io.h>
#include "sherpa-onnx/c-api/c-api.h"
static void *symbol(HMODULE dll,const char *name){void *p=(void *)GetProcAddress(dll,name);if(!p){fprintf(stderr,"Missing %s\n",name);exit(2);}return p;}
int main(int argc,char **argv){
 if(argc!=3)return 2;
 _setmode(_fileno(stdin),_O_BINARY);_setmode(_fileno(stdout),_O_BINARY);
 SetDllDirectoryA(argv[1]);char dllpath[4096];snprintf(dllpath,sizeof(dllpath),"%s\\sherpa-onnx-c-api.dll",argv[1]);HMODULE dll=LoadLibraryA(dllpath);if(!dll){fprintf(stderr,"Load C API: %lu\n",GetLastError());return 2;}
 const SherpaOnnxOfflineTts *(*create)(const SherpaOnnxOfflineTtsConfig *)=symbol(dll,"SherpaOnnxCreateOfflineTts");
 const SherpaOnnxGeneratedAudio *(*generate)(const SherpaOnnxOfflineTts *,const char *,int32_t,float)=symbol(dll,"SherpaOnnxOfflineTtsGenerate");
 void (*free_audio)(const SherpaOnnxGeneratedAudio *)=symbol(dll,"SherpaOnnxDestroyOfflineTtsGeneratedAudio");
 int32_t (*num_speakers)(const SherpaOnnxOfflineTts *)=symbol(dll,"SherpaOnnxOfflineTtsNumSpeakers");
 void (*destroy)(const SherpaOnnxOfflineTts *)=symbol(dll,"SherpaOnnxDestroyOfflineTts");
 char model[4096],voices[4096],tokens[4096],data[4096];
 snprintf(model,sizeof(model),"%s\\model.onnx",argv[2]);snprintf(voices,sizeof(voices),"%s\\voices.bin",argv[2]);snprintf(tokens,sizeof(tokens),"%s\\tokens.txt",argv[2]);snprintf(data,sizeof(data),"%s\\espeak-ng-data",argv[2]);
 SherpaOnnxOfflineTtsConfig config={0};config.model.kokoro.model=model;config.model.kokoro.voices=voices;config.model.kokoro.tokens=tokens;config.model.kokoro.data_dir=data;config.model.kokoro.lang="en";config.model.kokoro.length_scale=1;config.model.provider="cpu";config.model.num_threads=2;config.max_num_sentences=1;config.silence_scale=1;
 const SherpaOnnxOfflineTts *tts=create(&config);if(!tts)return 3;int32_t count=num_speakers(tts);if(count<1)return 3;
 uint32_t magic=0x4b4f4b4f;if(fwrite(&magic,4,1,stdout)!=1)return 3;fflush(stdout);
 uint32_t length;
 while(fread(&length,4,1,stdin)==1){if(!length||length>8192)break;uint32_t speaker;if(fread(&speaker,4,1,stdin)!=1||speaker>=(uint32_t)count)return 4;char text[8193];if(fread(text,1,length,stdin)!=length)break;text[length]=0;
  const SherpaOnnxGeneratedAudio *a=generate(tts,text,(int32_t)speaker,1.0f);if(!a||a->n<=0||a->n>24000*60)return 4;
  uint32_t header[2]={(uint32_t)a->sample_rate,(uint32_t)a->n};if(fwrite(header,4,2,stdout)!=2||fwrite(a->samples,4,a->n,stdout)!=(size_t)a->n)return 4;fflush(stdout);free_audio(a);
 }
 destroy(tts);return 0;
}
