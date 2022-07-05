import sys
import os
import re
import tabulate
import requests

"""
This program helps you to calculate the average performance given input logs.

NOTICE: You should run it under `src/raft` !!!!

Usage:
    $ python calc_logs.py [log file name]

Example:
    $ python calc_logs.py test2A.log.txt

It will compute the average of the test performance, and crawl the result of official
solution from the course website.

Finally it will present the result in github flavored markdown table
"""

def get_test_names(test_suite_name):
  test_names = []
  with open("test_test.go", "r", encoding="utf-8") as f:
    test_funcs = re.findall(f'func (.*{test_suite_name})', f.read())
    for test_func in test_funcs:
      if test_func.count("For2023") == 0:
        test_names.append(test_func)
  return test_names

def calc_avg(file_name, test_suite_name):
  test_names = []

  test_names = get_test_names(test_suite_name)
  for i in range(len(test_names)):
    test_names[i] = [test_names[i]] + [0]*5

  with open(file_name, "r", encoding="utf-8") as f:
    content = f.read()
    if len(re.findall(r'FAIL', content)) != 0:
      print(re.findall(r'FAIL', content))
      print(f"The log file: \"{file_name}\" has some failed test!!!")
      return
    # data format: [(time, servers, RPC Counts, RPC bytes, commits)]
    data = re.findall(r'Passed -- *(\d+.\d+) *(\d+) *(\d+) *(\d+) *(\d+)', content)
    rounds = len(re.findall(r'ok.*?6.824/raft.*?s', content, flags=re.DOTALL))
    for i in range(len(data)):
      for j in range(5):
        if j == 0:
          test_names[i%len(test_names)][j+1] += float(data[i][j])
        else:
          test_names[i%len(test_names)][j+1] += int(data[i][j])
    for i in range(len(test_names)):
      for j in range(1, 6, 1):
        test_names[i][j] /= rounds
        if j == 1:
          test_names[i][j] = f'{test_names[i][j]:.1f}'
        else:
          test_names[i][j] = int(test_names[i][j])
    test_names = [["Test Name", "Time", "Servers", "RPC Count", "Total Bytes", "Commits"]] + test_names
    print(tabulate.tabulate(test_names, headers="firstrow", tablefmt="github"))



def crawl_course_solution(test_name):
  url = "http://nil.lcs.mit.edu/6.824/2022/labs/lab-raft.html"
  web_page = requests.get(url).text
  with open("tmp", "w+", encoding='utf-8') as f:
    relevant_part = re.findall(f'<pre>.*go test -run {test_name}\n(.*?)</pre>', web_page, flags=re.DOTALL)
    print(relevant_part, file=f)

  calc_avg('tmp', test_name)
  os.remove('tmp')



def report_result(log_file_name, test_suite_name = None):
  if test_suite_name is None:
    with open(log_file_name, "r", encoding="utf-8") as f:
      first_line = f.readline()
      test_suite_name = re.findall(r'Test \((.*)\):', first_line)[0]
      print(test_suite_name)
  print("My solution:")
  calc_avg(log_file_name, test_suite_name)
  print("The instructors' solution:")
  crawl_course_solution(test_suite_name)


if __name__ == "__main__":
  for log_file_name in sys.argv[1:]:
    report_result(log_file_name)
