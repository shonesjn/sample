import pandas as pd
import numpy as np

df = pd.read_csv('scheduler_metrics.csv')
print('=== FULL DIAGNOSTIC REPORT (%d rows) ===' % len(df))

print()
print('--- FRAME DROP ANALYSIS ---')
tf = df['total_frames'].iloc[-1]
dr = df['dropped_frames'].iloc[-1]
print('Total Frames: %d, Dropped: %d, Drop Rate: %.1f%%' % (tf, dr, dr/max(tf,1)*100))
print('Dropped > Total (ABNORMAL):', dr > tf)

print()
print('--- LATENCY CLAMPING CHECK ---')
print('Latency max:', df['latency'].max(), '| min:', df['latency'].min(), '| mean:', round(df['latency'].mean(), 1))
print('Values == 9999 (clamped sentinel):', (df['latency'] == 9999).sum())
print('Values == 0 (untracked rows):', (df['latency'] == 0).sum())
print('End-to-End max:', round(df['end_to_end_latency_ms'].max(), 1), '| mean:', round(df['end_to_end_latency_ms'].mean(), 1))
print('E2E > 9000 rows:', (df['end_to_end_latency_ms'] > 9000).sum())

print()
print('--- THROUGHPUT CHECK ---')
print('Throughput: max=%.1f, mean=%.2f, zeros=%d' % (df['throughput'].max(), df['throughput'].mean(), (df['throughput']==0).sum()))
print('FPS col: max=%.1f, mean=%.2f' % (df['fps'].max(), df['fps'].mean()))

print()
print('--- NODE THROUGHPUT ---')
for n in [1, 2, 3]:
    col = 'node%d_throughput' % n
    print('  %s: max=%.1f, mean=%.2f, nonzero=%d' % (col, df[col].max(), df[col].mean(), (df[col]>0).sum()))

print()
print('--- SYSTEM BOUNDS CHECK ---')
print('CPU: max=%s, >100: %d' % (df['cpu'].max(), (df['cpu']>100).sum()))
print('Memory: max=%s, >100: %d' % (df['memory'].max(), (df['memory']>100).sum()))
print('Temperature: max=%s, min=%s' % (df['temperature'].max(), df['temperature'].min()))
print('Power: max=%s, min=%s' % (df['power'].max(), df['power'].min()))
print('QoS: max=%s, min=%s, unique=%d' % (df['qos'].max(), df['qos'].min(), df['qos'].nunique()))
print('Queue: max=%s, rows > 100: %d' % (df['queue'].max(), (df['queue']>100).sum()))

print()
print('--- 9-STAGE LATENCY BREAKDOWN (mean ms) ---')
stages = [
    's1_client_to_sched_ms','s2_sched_decision_ms','s3_sched_to_compute_ms',
    's4_compute_to_python_ms','s5_python_decode_ms','s6_yolo_inference_ms',
    's7_python_to_compute_ms','s8_compute_to_sched_ms','s9_sched_to_frontend_ms'
]
for s in stages:
    if s in df.columns:
        v = df[s]
        print('  %s: mean=%.2f, max=%.2f, zeros=%d' % (s, v.mean(), v.max(), (v==0).sum()))

print()
print('--- DQN / SCHEDULER OVERHEAD ---')
print('DQN inference: max=%.2f, unique values=%d' % (df['dqn_inference_ms'].max(), df['dqn_inference_ms'].nunique()))
print('Sched decision: max=%.3f, mean=%.4f' % (df['scheduler_decision_ms'].max(), df['scheduler_decision_ms'].mean()))

print()
print('--- POLICY DISTRIBUTION ---')
for p in ['policy0_frames','policy1_frames','policy2_frames','policy3_frames']:
    print('  %s: %d' % (p, df[p].iloc[-1]))
print('Selected_node unique values:', df['selected_node'].unique())

print()
print('--- NODE FRAMES ---')
for n in [1, 2, 3]:
    col = 'node%d_frames' % n
    print('  %s: %d' % (col, df[col].iloc[-1]))
