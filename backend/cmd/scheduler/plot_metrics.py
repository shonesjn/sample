#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
plot_metrics.py - Scheduler Metrics Dashboard
Usage: python3 plot_metrics.py /path/to/scheduler_metrics.csv
"""

import sys
import os
import re
import pandas as pd
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
import numpy as np


def safe_numeric(df, cols):
    for c in cols:
        if c in df.columns:
            df[c] = pd.to_numeric(df[c], errors='coerce')
    return df


def create_dashboard(df, output_path):
    numeric_cols = [
        'cpu', 'memory', 'temperature', 'queue', 'latency', 'throughput',
        'power', 'qos', 'dqn_inference_ms', 'exploration_ms', 'exploitation_ms',
        'scheduler_decision_ms', 'total_frames', 'dropped_frames', 'scheduler_queue_len',
        'node1_frames', 'node2_frames', 'node3_frames', 'node1_throughput',
        'node2_throughput', 'node3_throughput', 'avg_inference_latency',
        'policy0_frames', 'policy1_frames', 'policy2_frames', 'policy3_frames',
        'end_to_end_latency_ms', 'fps', 'scheduler_utilization',
        's1_client_to_sched_ms', 's2_sched_decision_ms', 's3_sched_to_compute_ms',
        's4_compute_to_python_ms', 's5_python_decode_ms', 's6_yolo_inference_ms',
        's7_python_to_compute_ms', 's8_compute_to_sched_ms', 's9_sched_to_frontend_ms',
        'node1_gpu', 'node2_gpu', 'node3_gpu'
    ]
    df = safe_numeric(df, numeric_cols)
    df['datetime'] = pd.to_datetime(df['timestamp'], unit='s')

    fig, axes = plt.subplots(4, 3, figsize=(18, 16))
    fig.suptitle('Scheduler Metrics Dashboard', fontsize=16, y=0.98)

    # Get latest values for titles
    last = df.iloc[-1] if len(df) > 0 else df.iloc[0]

    # 1. System: CPU & Memory
    ax = axes[0, 0]
    ax.plot(df['datetime'], df['cpu'], label='CPU %', linewidth=1.2)
    ax.plot(df['datetime'], df['memory'], label='Memory %', linewidth=1.2)
    ax.set_title('System: CPU=%.1f%%, Mem=%.1f%%' % (last['cpu'], last['memory']), fontsize=11)
    ax.legend(loc='center right')
    ax.grid(True)

    # 2. Temperature
    ax = axes[0, 1]
    ax.plot(df['datetime'], df['temperature'], color='tab:red', linewidth=1.2)
    ax.set_title('Temperature: %.1fC' % last['temperature'], fontsize=11)
    ax.grid(True)

    # 3. Power
    ax = axes[0, 2]
    ax.plot(df['datetime'], df['power'], color='tab:green', linewidth=1.2)
    ax.set_title('Power: %.2fW' % last['power'], fontsize=11)
    ax.grid(True)

    # 4. Queue Depth
    ax = axes[1, 0]
    ax.plot(df['datetime'], df['queue'], color='tab:purple', linewidth=1.2)
    ax.axhline(y=30, color='tab:red', linestyle='--', linewidth=1.5, label='Max')
    ax.set_title('Queue Depth: %d' % int(last['queue']), fontsize=11)
    ax.legend(loc='center right')
    ax.grid(True)

    # 5. Latency
    ax = axes[1, 1]
    ax.plot(df['datetime'], df['latency'], color='tab:brown', linewidth=1.2)
    ax.set_title('Latency: %.1fms' % last['latency'], fontsize=11)
    ax.grid(True)

    # 6. Throughput
    ax = axes[1, 2]
    ax.plot(df['datetime'], df['throughput'], color='tab:cyan', linewidth=1.2)
    ax.set_title('Throughput: %.2f fps' % last['throughput'], fontsize=11)
    ax.grid(True)

    # 7. Policy Selection Distribution (Categorical Bar Chart)
    ax = axes[2, 0]
    policy_cols = ['policy0_frames', 'policy1_frames', 'policy2_frames', 'policy3_frames']
    policy_cols = [c for c in policy_cols if c in df.columns and not df[c].isna().all()]
    if policy_cols:
        policies = df[policy_cols].iloc[-1]
        labels = []
        for p in policies.index:
            m = re.search(r'\d', str(p))
            num = int(m.group()) if m else 0
            labels.append('Policy %d' % num)
        colors = ['#1f77b4', '#ff7f0e', '#2ca02c', '#d62728']
        ax.bar(labels, policies.values, color=colors[:len(labels)], edgecolor='lightgray', width=0.8)
    ax.set_title('Policy Selection Distribution', fontsize=11)
    ax.tick_params(axis='x', labelsize=9)
    ax.grid(True)

    # 8. Total vs Dropped Frames
    ax = axes[2, 1]
    ax.plot(df['datetime'], df['total_frames'], label='Total', linewidth=1.5)
    ax.plot(df['datetime'], df['dropped_frames'], label='Dropped', color='tab:red', linewidth=1.5)
    ax.set_title('Frames: Total=%d, Dropped=%d' % (int(last['total_frames']), int(last['dropped_frames'])), fontsize=11)
    ax.legend(loc='upper left')
    ax.grid(True)

    # 9. Frames per Node
    ax = axes[2, 2]
    node_cols = ['node1_frames', 'node2_frames', 'node3_frames']
    node_cols = [c for c in node_cols if c in df.columns]
    for i, col in enumerate(node_cols):
        ax.plot(df['datetime'], df[col], label=col.replace('_frames', ''), linewidth=1.5)
    ax.set_title('Frames per Node', fontsize=11)
    ax.legend(loc='upper left')
    ax.grid(True)

    # 10. GPU Utilization per Node
    ax = axes[3, 0]
    gpu_cols = ['node1_gpu', 'node2_gpu', 'node3_gpu']
    gpu_cols = [c for c in gpu_cols if c in df.columns]
    for col in gpu_cols:
        ax.plot(df['datetime'], df[col], label=col.replace('_gpu', ''), linewidth=1.5)
    ax.set_title('GPU Utilization per Node', fontsize=11)
    ax.legend(loc='upper left')
    ax.grid(True)

    # 11. Throughput per Node
    ax = axes[3, 1]
    tp_cols = ['node1_throughput', 'node2_throughput', 'node3_throughput']
    tp_cols = [c for c in tp_cols if c in df.columns]
    for col in tp_cols:
        ax.plot(df['datetime'], df[col], label=col.replace('_throughput', ''), linewidth=1.5)
    ax.set_title('Throughput per Node (fps)', fontsize=11)
    ax.legend(loc='upper left')
    ax.grid(True)

    # 12. Scheduler Decisions Overhead
    ax = axes[3, 2]
    ax.plot(df['datetime'], df['dqn_inference_ms'], label='DQN Inference', linewidth=1.2)
    ax.plot(df['datetime'], df['scheduler_decision_ms'], label='Sched Decision', linewidth=1.2)
    ax.set_title('Scheduler Decisions Overhead', fontsize=11)
    ax.legend(loc='upper left')
    ax.grid(True)

    # Format time axes cleanly for all plots EXCEPT the Policy Distribution bar chart
    for r in range(4):
        for c in range(3):
            if r == 2 and c == 0:
                continue  # Skip the bar chart
            axes[r, c].xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
            axes[r, c].tick_params(axis='x', labelsize=9)

    plt.tight_layout(rect=[0, 0, 1, 0.95])
    plt.savefig(output_path, dpi=150, bbox_inches='tight')
    print('Dashboard saved to: ' + output_path)
    plt.close()


def save_individual_plots(df, plots_dir):
    os.makedirs(plots_dir, exist_ok=True)
    numeric_cols = [
        'cpu', 'memory', 'temperature', 'queue', 'latency', 'throughput',
        'power', 'qos', 'dqn_inference_ms', 'exploration_ms', 'exploitation_ms',
        'scheduler_decision_ms', 'total_frames', 'dropped_frames', 'scheduler_queue_len',
        'node1_frames', 'node2_frames', 'node3_frames', 'node1_throughput',
        'node2_throughput', 'node3_throughput', 'avg_inference_latency',
        'policy0_frames', 'policy1_frames', 'policy2_frames', 'policy3_frames',
        'end_to_end_latency_ms', 'fps', 'scheduler_utilization',
        'node1_gpu', 'node2_gpu', 'node3_gpu'
    ]
    df = safe_numeric(df, numeric_cols)
    df['datetime'] = pd.to_datetime(df['timestamp'], unit='s')

    def setup_ax(fig, title, ylabel):
        ax = fig.add_subplot(111)
        ax.set_title(title, fontsize=12, fontweight='bold')
        ax.set_ylabel(ylabel, fontsize=10)
        ax.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
        ax.grid(True, linestyle='--', alpha=0.6)
        return ax

    # 1. CPU
    fig = plt.figure(figsize=(8, 5))
    ax = setup_ax(fig, 'CPU Utilization Over Time', 'CPU %')
    ax.plot(df['datetime'], df['cpu'], color='tab:blue', label='CPU %')
    ax.legend()
    fig.tight_layout()
    fig.savefig(os.path.join(plots_dir, 'cpu_vs_time.png'), dpi=150)
    plt.close(fig)

    # 2. Memory
    fig = plt.figure(figsize=(8, 5))
    ax = setup_ax(fig, 'Memory Utilization Over Time', 'Memory %')
    ax.plot(df['datetime'], df['memory'], color='tab:orange', label='Memory %')
    ax.legend()
    fig.tight_layout()
    fig.savefig(os.path.join(plots_dir, 'memory_vs_time.png'), dpi=150)
    plt.close(fig)

    # 3. Temperature
    fig = plt.figure(figsize=(8, 5))
    ax = setup_ax(fig, 'Temperature Over Time', 'Temperature (°C)')
    ax.plot(df['datetime'], df['temperature'], color='tab:red', label='Temp (°C)')
    ax.legend()
    fig.tight_layout()
    fig.savefig(os.path.join(plots_dir, 'temperature_vs_time.png'), dpi=150)
    plt.close(fig)

    # 4. Power
    fig = plt.figure(figsize=(8, 5))
    ax = setup_ax(fig, 'Power Consumption Over Time', 'Power (W)')
    ax.plot(df['datetime'], df['power'], color='tab:green', label='Power (W)')
    ax.legend()
    fig.tight_layout()
    fig.savefig(os.path.join(plots_dir, 'power_vs_time.png'), dpi=150)
    plt.close(fig)

    # 5. Queue Depth
    fig = plt.figure(figsize=(8, 5))
    ax = setup_ax(fig, 'Queue Depth Over Time', 'Queue Frames')
    ax.plot(df['datetime'], df['queue'], color='tab:purple', label='Queue Depth')
    ax.axhline(y=30, color='tab:red', linestyle='--', linewidth=1.5, label='Max (30)')
    ax.legend()
    fig.tight_layout()
    fig.savefig(os.path.join(plots_dir, 'queue_vs_time.png'), dpi=150)
    plt.close(fig)

    # 6. Latency
    fig = plt.figure(figsize=(8, 5))
    ax = setup_ax(fig, 'Latency Over Time', 'Latency (ms)')
    ax.plot(df['datetime'], df['latency'], color='tab:brown', label='Latency (ms)')
    ax.legend()
    fig.tight_layout()
    fig.savefig(os.path.join(plots_dir, 'latency_vs_time.png'), dpi=150)
    plt.close(fig)

    # 7. Throughput
    fig = plt.figure(figsize=(8, 5))
    ax = setup_ax(fig, 'Throughput Over Time', 'Throughput (fps)')
    ax.plot(df['datetime'], df['throughput'], color='tab:cyan', label='Throughput (fps)')
    ax.legend()
    fig.tight_layout()
    fig.savefig(os.path.join(plots_dir, 'throughput_vs_time.png'), dpi=150)
    plt.close(fig)

    # 8. QoS
    fig = plt.figure(figsize=(8, 5))
    ax = setup_ax(fig, 'QoS Score Over Time', 'QoS Score')
    ax.plot(df['datetime'], df['qos'], color='tab:olive', label='QoS')
    ax.legend()
    fig.tight_layout()
    fig.savefig(os.path.join(plots_dir, 'qos_vs_time.png'), dpi=150)
    plt.close(fig)

    # 9. Frames Processed vs Dropped
    fig = plt.figure(figsize=(8, 5))
    ax = setup_ax(fig, 'Frames Processed vs Dropped', 'Frames')
    ax.plot(df['datetime'], df['total_frames'], label='Total Frames', color='tab:blue')
    ax.plot(df['datetime'], df['dropped_frames'], label='Dropped Frames', color='tab:red')
    ax.legend()
    fig.tight_layout()
    fig.savefig(os.path.join(plots_dir, 'frames_processed_vs_time.png'), dpi=150)
    plt.close(fig)

    # 10. Node Selection / Throughput per Node
    fig = plt.figure(figsize=(8, 5))
    ax = setup_ax(fig, 'Throughput per Node', 'FPS')
    tp_cols = ['node1_throughput', 'node2_throughput', 'node3_throughput']
    tp_cols = [c for c in tp_cols if c in df.columns]
    for col in tp_cols:
        ax.plot(df['datetime'], df[col], label=col.replace('_throughput', ''), linewidth=1.5)
    ax.legend()
    fig.tight_layout()
    fig.savefig(os.path.join(plots_dir, 'node_selection.png'), dpi=150)
    plt.close(fig)

    # 11. Policy Usage
    fig = plt.figure(figsize=(8, 5))
    ax = fig.add_subplot(111)
    policy_cols = ['policy0_frames', 'policy1_frames', 'policy2_frames', 'policy3_frames']
    policy_cols = [c for c in policy_cols if c in df.columns and not df[c].isna().all()]
    if policy_cols:
        policies = df[policy_cols].iloc[-1]
        labels = []
        for p in policies.index:
            m = re.search(r'\d', str(p))
            labels.append('Policy %s' % (m.group() if m else '0'))
        colors = ['#1f77b4', '#ff7f0e', '#2ca02c', '#d62728']
        ax.bar(labels, policies.values, color=colors[:len(labels)], edgecolor='lightgray', width=0.6)
    ax.set_title('Policy Selection Distribution', fontsize=12, fontweight='bold')
    ax.set_ylabel('Total Decisions', fontsize=10)
    ax.grid(True, linestyle='--', alpha=0.6)
    fig.tight_layout()
    fig.savefig(os.path.join(plots_dir, 'policy_usage.png'), dpi=150)
    plt.close(fig)

    # 12. Decision & DQN Overheads
    fig = plt.figure(figsize=(8, 5))
    ax = setup_ax(fig, 'DQN Inference Time Over Time', 'Time (ms)')
    ax.plot(df['datetime'], df['dqn_inference_ms'], label='DQN Inference', color='tab:purple')
    ax.legend()
    fig.tight_layout()
    fig.savefig(os.path.join(plots_dir, 'dqn_inference_time_vs_time.png'), dpi=150)
    plt.close(fig)

    fig = plt.figure(figsize=(8, 5))
    ax = setup_ax(fig, 'Scheduler Decision Time Over Time', 'Time (ms)')
    ax.plot(df['datetime'], df['scheduler_decision_ms'], label='Scheduler Decision', color='tab:orange')
    ax.legend()
    fig.tight_layout()
    fig.savefig(os.path.join(plots_dir, 'scheduler_decision_time_vs_time.png'), dpi=150)
    plt.close(fig)

    print(f'Saved all individual plots to: {plots_dir}')


def main():
    if len(sys.argv) < 2:
        print('Usage: python3 plot_metrics.py <path/to/scheduler_metrics.csv>')
        sys.exit(1)

    csv_path = sys.argv[1]
    if not os.path.exists(csv_path):
        print('Error: File not found: ' + csv_path)
        sys.exit(1)

    df = pd.read_csv(csv_path)
    print('Loaded %d rows from %s' % (len(df), csv_path))
    print('Columns: %s' % str(list(df.columns)))

    output_path = os.path.splitext(csv_path)[0] + '_dashboard.png'
    create_dashboard(df, output_path)

    # Save to both ./plots and cmd/scheduler/plots
    script_dir = os.path.dirname(os.path.abspath(__file__))
    cmd_plots_dir = os.path.join(script_dir, 'plots')
    save_individual_plots(df, cmd_plots_dir)

    base_dir = os.path.dirname(csv_path) or '.'
    local_plots_dir = os.path.join(base_dir, 'plots')
    if os.path.abspath(local_plots_dir) != os.path.abspath(cmd_plots_dir):
        save_individual_plots(df, local_plots_dir)


if __name__ == '__main__':
    main()